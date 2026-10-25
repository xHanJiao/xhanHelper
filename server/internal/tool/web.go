package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/config"
)

const (
	// webQuotaProvider — 每日额度的计费主体名。expires_at 设为明日零点，靠 SearchUsageRepo
	// 「expires_at 变化即清零」的机制实现按天滚动（见 service/search_usage_repo.go）。
	webQuotaProvider = "deepseek_web"
	webMaxCount      = 20  // 模型可选条数上限（API 支持 50，此处按 token 预算收紧）
	webMaxQueryRunes = 128 // API 硬限制
	webMaxRespBytes  = 8 << 20
	webRetryDelay    = 800 * time.Millisecond
	webProbeTimeout  = 5 * time.Second // 上游 404 后本地探测的超时
)

// WebQuota — 每日调用额度的最小依赖。生产实现 = service.SearchUsageRepo。
type WebQuota interface {
	TryUse(ctx context.Context, provider string, totalQuota int, expiresAt time.Time, initialUsed int) (int, error)
}

// webUpstreamError — 上游/网络失败的结构化信息：既要给模型可读文案，也要给可判断的字段。
type webUpstreamError struct {
	Status    int    // 上游 HTTP 状态码；网络错误为 0
	Reason    string // 分类：not_found / dns_error / blocked / unsupported_content / error_page ...
	Message   string // 上游 error.message（已剔除 request_id 与本地探测注记）
	RequestID string // 上游 request_id，便于找官方核对
}

func (e *webUpstreamError) Error() string {
	return fmt.Sprintf("status=%d reason=%s message=%s", e.Status, e.Reason, e.Message)
}

// WebTool — 联网工具：DeepSeek 官方 Search（网页搜索）+ Fetch（网页正文抓取）。
// 主循环可见（经 expose_tools 按需暴露），两个函数均只读。
type WebTool struct {
	client          *http.Client
	baseURL         string
	apiKey          string
	searchCount     int
	maxContentChars int
	dailyLimit      int      // <0 = 不限制
	quota           WebQuota // nil = 不限制
	retryDelay      time.Duration
	// probe — 上游 fetch 失败后本地探一次，用于区分 dns/超时/反爬/不支持的类型；测试可替换。
	probe func(ctx context.Context, target string) (reason, detail string)
}

// NewWebTool 创建联网工具。quota 为 nil 或 cfg.DailyLimit < 0 时不做额度限制。
func NewWebTool(cfg config.WebToolsConfig, quota WebQuota) *WebTool {
	return &WebTool{
		client:          &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:          cfg.APIKey,
		searchCount:     cfg.SearchCount,
		maxContentChars: cfg.MaxContentChars,
		dailyLimit:      cfg.DailyLimit,
		quota:           quota,
		retryDelay:      webRetryDelay,
		probe:           probeFetchTarget,
	}
}

func (t *WebTool) Name() string { return "web" }

func (t *WebTool) Description() string {
	return "联网：search 搜索网页，fetch 抓取指定 URL 的正文（已清洗为 Markdown）。" +
		"【成本】每次调用都按次计费、结果全部进入上下文：先用 search 看摘要挑来源，只在必须读原文时对选中的 URL 调 fetch，不要批量抓取多个页面。" +
		"需要 JS 渲染、点击交互的多页调研请派 task 子任务 web_explore（需 Mac 客户端在线）。"
}

func (t *WebTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "联网。搜索网页 / 抓取网页正文（DeepSeek 官方 Search/Fetch）。",
		Funcs:     []string{"search", "fetch"},
	}, true
}

func (t *WebTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *WebTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:    "search",
			Display: "联网搜索",
			Description: "按关键词搜索网页，返回标题、链接与长摘要（约 600–800 字，够判断是否值得细读）。" +
				"query 支持搜索语法：site:github.com、-site:example.com 排除站点、-词 排除关键词、\"精确短语\"、lang:zh,en、after:2025-01-01、before:2026-06-01。" +
				"结果里的 date 是上架/索引时间，可能不准，不要当作发布时间下断言。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "搜索词（≤128 字符），可用 site:/after:/before:/lang:/引号等语法收窄范围",
					},
					"count": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("返回条数，默认 %d，最多 %d（API 支持 50，此处为省 token 收紧）；不传或非正数按默认", t.searchCount, webMaxCount),
					},
					"need_content": map[string]interface{}{
						"type":        "boolean",
						"description": "是否同时返回正文（默认 false）。正文很占 token，通常改为对选中的 URL 调 fetch",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "fetch",
			Display:     "抓取网页",
			Description: "抓取单个网页正文，返回 Markdown。适合官方文档/文章/百科等静态页；JS 渲染页、反爬站点、PDF 等非网页内容会失败（失败结果带 upstream_status 与 reason：dns_error/timeout/blocked/unsupported_content/not_found/error_page，便于决定重试还是换来源）。结果会截断到配置上限。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "网页 URL（完整的 http/https 地址）",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (t *WebTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	switch funcName {
	case "search":
		return t.search(ec.Ctx, args)
	case "fetch":
		return t.fetch(ec.Ctx, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
	}
}

// ---------- search ----------

type webSearchItem struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Summary  string `json:"summary"`
	SiteName string `json:"site_name,omitempty"`
	Date     string `json:"date,omitempty"`
	Content  string `json:"content,omitempty"`
}

func (t *WebTool) search(ctx context.Context, args json.RawMessage) *Result {
	var a struct {
		Query       string `json:"query"`
		Count       int    `json:"count"`
		NeedContent bool   `json:"need_content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if utf8.RuneCountInString(query) > webMaxQueryRunes {
		query = truncateRunes(query, webMaxQueryRunes)
	}
	count := a.Count
	if count <= 0 {
		count = t.searchCount
	}
	if count > webMaxCount {
		count = webMaxCount
	}

	if msg := t.useQuota(ctx); msg != "" {
		return &Result{Success: false, Error: msg}
	}

	raw, err := t.post(ctx, "/v1/tools/search", map[string]interface{}{
		"query":        query,
		"count":        count,
		"need_content": a.NeedContent,
	})
	if err != nil {
		return t.fail(err)
	}

	var resp struct {
		Results []webSearchRaw `json:"results"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		log.Printf("[web] search 响应解析失败: %v", err)
		return &Result{Success: false, Error: "搜索服务返回了无法解析的内容"}
	}

	items := t.shapeSearchResults(resp.Results)
	out := struct {
		Results   []webSearchItem `json:"results"`
		Truncated bool            `json:"truncated,omitempty"`
		Note      string          `json:"note,omitempty"`
	}{}
	if len(items) == 0 {
		out.Results = []webSearchItem{}
		out.Note = "没有匹配结果（或结果质量过低被过滤）。可换个关键词，或去掉 site:/after: 等限定后重试"
	}
	// 总预算：摘要 + 正文合计不超过 maxContentChars；超了就不再追加后续条目。
	budget := t.maxContentChars
	for _, item := range items {
		cost := utf8.RuneCountInString(item.Summary) + utf8.RuneCountInString(item.Content)
		if len(out.Results) > 0 && budget-cost < 0 {
			out.Truncated = true
			break
		}
		budget -= cost
		out.Results = append(out.Results, item)
	}

	data, err := json.Marshal(out)
	if err != nil {
		log.Printf("[web] search 结果序列化失败: %v", err)
		return &Result{Success: false, Error: "搜索结果序列化失败"}
	}
	return &Result{Success: true, Data: data}
}

// webSearchRaw — 上游 search 结果里我们关心的字段（其余 snippet/site_icon/authority 不透出）。
type webSearchRaw struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Summary     string  `json:"summary"`
	Content     string  `json:"content"`
	SiteName    *string `json:"site_name"`
	Date        *string `json:"date"`
	RerankScore float64 `json:"rerank_score"`
}

// shapeSearchResults 把上游结果清洗成给模型的样子：
//   - 丢空标题、按清洗后的 URL 去重
//   - 去掉 utm/affiliate 等跟踪参数（上游会返回同文不同源的脏链接）
//   - 官方说 rerank_score ≥1.5 为优质；留 1.0 当底线，但至少保留 3 条（避免把长尾查询清空）
//   - 正文做 Markdown 转义还原（上游清洗器会把标点全转义，直接把 JSON/表格/标题毁掉）
func (t *WebTool) shapeSearchResults(in []webSearchRaw) []webSearchItem {
	items := make([]webSearchItem, 0, len(in))
	scores := make([]float64, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, r := range in {
		title := strings.TrimSpace(unescapeMarkdown(r.Title))
		if title == "" {
			continue
		}
		link := cleanSearchURL(strings.TrimSpace(r.URL))
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true

		item := webSearchItem{Title: title, URL: link, Summary: r.Summary}
		if r.Content != "" {
			item.Content = truncateRunes(unescapeMarkdown(r.Content), t.maxContentChars)
		}
		if r.SiteName != nil {
			item.SiteName = *r.SiteName
		}
		if r.Date != nil {
			item.Date = *r.Date
		}
		items = append(items, item)
		scores = append(scores, r.RerankScore)
	}

	if len(items) > 3 {
		keptItems := make([]webSearchItem, 0, len(items))
		for i, item := range items {
			if scores[i] < 1.0 {
				continue
			}
			keptItems = append(keptItems, item)
		}
		if len(keptItems) >= 3 {
			items = keptItems
		}
	}
	return items
}

// webTrackingParams 需要从结果 URL 里剥掉的跟踪/联盟参数（只保留确实无害的常见集合）。
var webTrackingParams = map[string]bool{
	"gclid": true, "fbclid": true, "spm": true, "scm": true,
	"share_token": true, "partner": true, "aff": true, "affiliate": true,
	"p_source": true, "p_type": true, "via": true,
}

func cleanSearchURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	changed := false
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || webTrackingParams[lk] {
			q.Del(k)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ---------- fetch ----------

func (t *WebTool) fetch(ctx context.Context, args json.RawMessage) *Result {
	var a struct {
		URL string `json:"url"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	target := strings.TrimSpace(a.URL)
	if msg := validateWebURL(target); msg != "" {
		return &Result{Success: false, Error: msg}
	}

	if msg := t.useQuota(ctx); msg != "" {
		return &Result{Success: false, Error: msg}
	}

	raw, err := t.post(ctx, "/v1/tools/fetch", map[string]string{"url": target})
	if err != nil {
		// 上游 404 只有一句 "failed to fetch URL"，本地探一次才能区分 dns/超时/反爬/不支持的类型
		var ue *webUpstreamError
		if errors.As(err, &ue) && ue.Status == http.StatusNotFound {
			reason, detail := t.probe(ctx, target)
			ue.Reason = reason
			if detail != "" {
				ue.Message = strings.TrimSpace(ue.Message + "；本地探测：" + detail)
			}
		}
		return t.fail(err)
	}

	var resp struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		log.Printf("[web] fetch 响应解析失败: %v", err)
		return &Result{Success: false, Error: "抓取服务返回了无法解析的内容"}
	}

	title := unescapeMarkdown(resp.Title)
	content := unescapeMarkdown(resp.Content)

	// 上游对 SPA/软 404 会返回 200 + 站点外壳（实测 api-docs 的死链就是这样）：正文短且带 not-found 文案
	if looksLikeWebErrorPage(title, content) {
		return t.failResult(http.StatusOK, "error_page",
			fmt.Sprintf("标题「%s」、正文 %d 字符", title, utf8.RuneCountInString(content)), "")
	}

	truncated := utf8.RuneCountInString(content) > t.maxContentChars
	content = truncateRunes(content, t.maxContentChars)
	note := ""
	if content == "" {
		note = "该页面没有可提取的正文"
	}

	data, err := json.Marshal(struct {
		URL       string `json:"url"`
		Title     string `json:"title"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated,omitempty"`
		Note      string `json:"note,omitempty"`
	}{URL: target, Title: title, Content: content, Truncated: truncated, Note: note})
	if err != nil {
		log.Printf("[web] fetch 结果序列化失败: %v", err)
		return &Result{Success: false, Error: "抓取结果序列化失败"}
	}
	return &Result{Success: true, Data: data}
}

var webErrorPageMarkers = []string{
	"page not found", "404 not found", "页面不存在", "页面未找到", "该页面不存在", "找不到页面", "页面没有找到",
}

// looksLikeWebErrorPage 识别「上游 200 但内容是错误页」：正文很短 + 命中 not-found 文案。
// 阈值 800 字符是为了不误伤正常短页面（错误页通常只剩导航壳）。
func looksLikeWebErrorPage(title, content string) bool {
	if utf8.RuneCountInString(content) >= 800 {
		return false
	}
	low := strings.ToLower(title + "\n" + content)
	for _, m := range webErrorPageMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// validateWebURL 校验抓取目标，返回非空字符串即失败原因。
func validateWebURL(raw string) string {
	if raw == "" {
		return "url 不能为空"
	}
	if len(raw) > 8192 {
		return "URL 过长（上限 8192 字符）"
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Sprintf("无效的 URL（需含 host 的 http/https 地址）: %s", raw)
	}
	if u.User != nil {
		return "URL 不允许携带用户名或密码"
	}
	return ""
}

// ---------- 本地探测（仅用于给 fetch 失败分类） ----------

// probeFetchTarget 本地探一次目标站点，把上游那句无类型的 404 拆成可行动的原因。
// 只取响应头，不解析正文；失败一律降级成粗分类，不让探测错误顶掉主错误。
func probeFetchTarget(ctx context.Context, target string) (reason, detail string) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return "bad_url", ""
	}
	ctx, cancel := context.WithTimeout(ctx, webProbeTimeout)
	defer cancel()

	ips, err := net.DefaultResolver.LookupHost(ctx, u.Hostname())
	if err != nil {
		return "dns_error", err.Error()
	}
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil || isBlockedWebIP(ip) {
			// 不让模型把联网工具当内网探测器用
			return "private_address", "解析到受限地址: " + s
		}
	}

	// 用 GET + Range 而不是 HEAD：不少站点对 HEAD 回 405，Range 只取首字节，代价可接受。
	// shortcut: 只校验首跳 DNS 与字面 IP，未防 DNS rebinding；要防需在 Dial 层固定解析结果。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "bad_url", err.Error()
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36")
	req.Header.Set("Range", "bytes=0-0")

	resp, err := (&http.Client{Timeout: webProbeTimeout}).Do(req)
	if err != nil {
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return "timeout", err.Error()
		}
		return "unreachable", err.Error()
	}
	resp.Body.Close()

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusTooManyRequests:
		return "blocked", fmt.Sprintf("本地访问返回 %d（反爬/需登录）", resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return "not_found", fmt.Sprintf("本地访问返回 %d", resp.StatusCode)
	case resp.StatusCode >= 500:
		return "target_error", fmt.Sprintf("本地访问返回 %d", resp.StatusCode)
	case isBinaryWebContentType(ct):
		return "unsupported_content", "Content-Type: " + ct
	case resp.StatusCode < 400:
		// 我们这边打得通，说明是抓取服务侧的问题（其网络受限或被其策略拦），换来源或稍后重试都可能有效
		return "upstream_only", fmt.Sprintf("本地访问正常（%d），抓取服务侧失败", resp.StatusCode)
	default:
		return "target_error", fmt.Sprintf("本地访问返回 %d", resp.StatusCode)
	}
}

func isBlockedWebIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func isBinaryWebContentType(ct string) bool {
	for _, p := range []string{"application/pdf", "application/zip", "application/octet-stream",
		"application/x-", "image/", "video/", "audio/"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}

// ---------- 额度 ----------

// useQuota 预扣一次调用额度，返回非空字符串即失败文案（可直接展示给模型）。
// 口径：请求前预扣 —— 失败的请求也占额度（比 DeepSeek「成功才计费」保守，日上限量级下可接受）。
func (t *WebTool) useQuota(ctx context.Context) string {
	if t.quota == nil || t.dailyLimit < 0 {
		return ""
	}
	now := time.Now()
	expiresAt := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	used, err := t.quota.TryUse(ctx, webQuotaProvider, t.dailyLimit, expiresAt, 0)
	if err != nil {
		// 额度用尽也走 error（repo 的设计），靠 used 达到上限区分「正常超限」与「DB 故障」
		if used >= t.dailyLimit {
			return fmt.Sprintf("今日联网额度已用完（%d/%d，明日 00:00 重置）。不要再重试；需要继续联网请派 task 子任务（subagent_type=web_explore，需 Mac 客户端在线）。", used, t.dailyLimit)
		}
		// 不把 DB 内部细节（表名/索引名/SQL）喂给模型，技术细节只进日志
		log.Printf("[web] 额度校验失败: %v", err)
		return "额度校验失败，本次联网未执行（服务端错误，已记录日志）"
	}
	return ""
}

// ---------- 错误呈现 ----------

// fail 把 error 转成 Result：人类可读文案进 Error，可机器判断的字段进 Data。
func (t *WebTool) fail(err error) *Result {
	var ue *webUpstreamError
	if errors.As(err, &ue) {
		return t.failResult(ue.Status, ue.Reason, ue.Message, ue.RequestID)
	}
	log.Printf("[web] 未分类失败: %v", err)
	return &Result{Success: false, Error: "请求失败，稍后重试"}
}

func (t *WebTool) failResult(status int, reason, detail, requestID string) *Result {
	data, err := json.Marshal(struct {
		UpstreamStatus int    `json:"upstream_status"`
		Reason         string `json:"reason"`
		Detail         string `json:"detail,omitempty"`
		RequestID      string `json:"request_id,omitempty"`
	}{UpstreamStatus: status, Reason: reason, Detail: detail, RequestID: requestID})
	if err != nil {
		log.Printf("[web] 失败结果序列化失败: %v", err)
		return &Result{Success: false, Error: webFailMessage(status, reason, detail, requestID)}
	}
	return &Result{Success: false, Error: webFailMessage(status, reason, detail, requestID), Data: data}
}

// webFailMessage 把分类翻成模型可据此行动的文案。
func webFailMessage(status int, reason, detail, requestID string) string {
	var msg string
	switch reason {
	case "bad_request":
		msg = "请求不合法（400）"
	case "bad_key":
		msg = "DeepSeek API Key 无效（401），联网不可用"
	case "insufficient_balance":
		msg = "DeepSeek 余额不足（402），联网不可用"
	case "blocked":
		msg = "目标站点拒绝访问（反爬/需登录），换其他来源"
	case "rate_limited":
		msg = "触发限速（429），稍后再试或减少调用次数"
	case "timeout":
		msg = "请求超时，稍后重试"
	case "canceled":
		msg = "请求已取消"
	case "network_error":
		msg = "网络请求失败，稍后重试"
	case "dns_error":
		msg = "域名解析失败（域名不存在或 DNS 不可用），核对 URL"
	case "private_address":
		msg = "URL 指向内网地址，已拒绝（只允许公网页面）"
	case "unsupported_content":
		msg = "目标不是可抓取的网页（PDF/压缩包/二进制等），换其他来源"
	case "not_found":
		msg = "目标页面不存在（404），核对 URL"
	case "target_error":
		msg = "目标站点返回错误，稍后或换来源重试"
	case "upstream_only":
		msg = "目标站点本身可访问，是抓取服务侧的失败（其网络受限或被其策略拦），换来源或稍后重试"
	case "error_page":
		msg = "抓到的是一张错误页（上游返回 200，正文是 404/空壳页），换来源或核对 URL"
	case "upstream_error":
		msg = "联网服务暂时不可用（5xx），稍后重试"
	default:
		if status > 0 {
			msg = fmt.Sprintf("联网请求失败（%d）", status)
		} else {
			msg = "联网请求失败"
		}
	}
	if detail != "" {
		msg += "；" + detail
	}
	if requestID != "" {
		msg += "（request_id: " + requestID + "）"
	}
	return msg
}

// ---------- HTTP ----------

var webRequestIDRe = regexp.MustCompile(`\s*\(request_id:\s*([0-9a-zA-Z-]+)\)`)

// post 发送 JSON 请求，返回响应体。对 408/429/5xx 自动重试一次（官方建议）。
// 失败一律返回 *webUpstreamError（含可判断的 reason 与上游 request_id），技术细节就地打日志。
func (t *WebTool) post(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[web] 请求序列化失败: %v", err)
		return nil, &webUpstreamError{Reason: "bad_request", Message: err.Error()}
	}

	status, data, err := t.doOnce(ctx, path, body)
	if err == nil && retryableWebStatus(status) {
		select {
		case <-time.After(t.retryDelay):
		case <-ctx.Done():
			return nil, &webUpstreamError{Reason: "canceled"}
		}
		status, data, err = t.doOnce(ctx, path, body)
	}
	if err != nil {
		log.Printf("[web] %s 请求失败: %v", path, err)
		return nil, wrapWebNetworkError(err)
	}
	if status == http.StatusOK {
		return data, nil
	}

	log.Printf("[web] %s HTTP %d: %s", path, status, strings.TrimSpace(string(data)))
	ue := &webUpstreamError{Status: status, Reason: webReasonForStatus(status)}
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(data, &e); jerr == nil && e.Error.Message != "" {
		clean, rid := splitWebRequestID(e.Error.Message)
		ue.Message, ue.RequestID = clean, rid
	}
	return nil, ue
}

func wrapWebNetworkError(err error) *webUpstreamError {
	e := &webUpstreamError{Reason: "network_error", Message: err.Error()}
	var nerr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		e.Reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &nerr) && nerr.Timeout():
		e.Reason = "timeout"
	}
	return e
}

// splitWebRequestID 把上游 message 里的 "(request_id: xxx)" 摘出来单独展示。
func splitWebRequestID(msg string) (string, string) {
	m := webRequestIDRe.FindStringSubmatch(msg)
	if m == nil {
		return strings.TrimSpace(msg), ""
	}
	return strings.TrimSpace(webRequestIDRe.ReplaceAllString(msg, "")), m[1]
}

func (t *WebTool) doOnce(ctx context.Context, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, webMaxRespBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, data, nil
}

func retryableWebStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func webReasonForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "bad_key"
	case http.StatusPaymentRequired:
		return "insufficient_balance"
	case http.StatusForbidden:
		return "blocked"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusRequestTimeout:
		return "timeout"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		if status >= 500 {
			return "upstream_error"
		}
		return "http_error"
	}
}

// ---------- 文本工具 ----------

// webMarkdownEscapeRe 匹配 CommonMark 的可转义字符：反斜杠 + ASCII 标点。
var webMarkdownEscapeRe = regexp.MustCompile(`\\([[:punct:]])`)

// unescapeMarkdown 还原上游清洗器加的转义（它会把 `{"id":`、`## 标题`、`|` 全部转义，
// 不还原则 JSON 无法解析、表格与标题层级直接毁掉）。代价：代码块里本想显示字面 `\.` 的会被还原。
func unescapeMarkdown(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return webMarkdownEscapeRe.ReplaceAllString(s, "$1")
}

// truncateRunes 按字符（rune）截断，避免切在多字节字符中间。
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}
