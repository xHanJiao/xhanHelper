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
	"unicode"
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
	Stage        string // 哪一层给出结论：api（DeepSeek 接口）/ target（目标站点或到它的网络）/ page（内容质量问题）/ local（我们自己的拦截）
	Status       int    // 仅 stage=api 时有意义：DeepSeek 接口的 HTTP 状态码
	TargetStatus int    // 仅 stage=target 且 ≥400 时有意义：目标站的 HTTP 状态码
	Reason       string // 分类：not_found / dns_error / blocked / unsupported_content / error_page ...
	Message      string // 上游 error.message（已剔除 request_id 与本地探测注记）
	RequestID    string // 上游 request_id，便于找官方核对
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
	searchMaxChars  int
	maxContentChars int
	dailyLimit      int      // <0 = 不限制
	quota           WebQuota // nil = 不限制
	retryDelay      time.Duration
	// probe — 上游 fetch 失败后本地探一次，用于区分 dns/超时/反爬/不支持的类型；测试可替换。
	probe func(ctx context.Context, target string) (reason, detail string, targetStatus int)
}

// NewWebTool 创建联网工具。quota 为 nil 或 cfg.DailyLimit < 0 时不做额度限制。
func NewWebTool(cfg config.WebToolsConfig, quota WebQuota) *WebTool {
	return &WebTool{
		client:          &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:          cfg.APIKey,
		searchCount:     cfg.SearchCount,
		searchMaxChars:  cfg.SearchMaxChars,
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
				"问时效性问题（本月/最近/今年/最新）必须带 recent——搜索器不会自己把「10月」理解成今年。" +
				"结果里的 date 是上架/索引时间，可能不准，不要当作发布时间下断言；返回体里的 today 是服务端当天日期。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "搜索词（≤128 字符），可用 site:/after:/before:/lang:/引号等语法收窄范围",
					},
					"count": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("返回条数，1–%d（不传用默认 %d）。超出上限会夹取，返回体里的 requested_count/returned_count 会说明实际值", webMaxCount, t.searchCount),
					},
					"recent": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"day", "week", "month", "year"},
						"description": "只取最近一段时间的网页（day/week/month/year）。时效敏感的问题必须带，工具会自动换算成 after:日期",
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
			Description: "抓取单个网页正文，返回 Markdown。适合官方文档/文章/百科等静态页；JS 渲染页、反爬站点、PDF 等非网页内容会失败（失败结果带 stage 与 reason：dns_error/timeout/blocked/unsupported_content/not_found/error_page，便于决定重试还是换来源）。结果会截断到配置上限。",
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
	Summary  string `json:"summary,omitempty"`
	SiteName string `json:"site_name,omitempty"`
	Date     string `json:"date,omitempty"`
	Content  string `json:"content,omitempty"`
}

func (t *WebTool) search(ctx context.Context, args json.RawMessage) *Result {
	var a struct {
		Query       string `json:"query"`
		Count       *int   `json:"count"` // 指针：区分「没传」与显式 count=0
		Recent      string `json:"recent"`
		NeedContent bool   `json:"need_content"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return &Result{Success: false, Error: "query 不能为空"}
	}
	if n := utf8.RuneCountInString(query); n > webMaxQueryRunes {
		return &Result{Success: false, Error: fmt.Sprintf(
			"query 过长（%d 字符，上限 %d）。请提炼关键词后重试，不要整段贴入", n, webMaxQueryRunes)}
	}

	requested := t.searchCount
	if a.Count != nil {
		requested = *a.Count
		if requested < 1 {
			return &Result{Success: false, Error: fmt.Sprintf("count 需 ≥1（不传用默认 %d，上限 %d）", t.searchCount, webMaxCount)}
		}
	}
	count := requested
	if count > webMaxCount {
		count = webMaxCount
	}

	if a.Recent != "" {
		days, ok := webRecentWindows[strings.ToLower(strings.TrimSpace(a.Recent))]
		if !ok {
			return &Result{Success: false, Error: "recent 只能是 day/week/month/year"}
		}
		query = appendWebAfter(query, days)
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
		Today          string          `json:"today"`
		RequestedCount int             `json:"requested_count"`
		ReturnedCount  int             `json:"returned_count"`
		Results        []webSearchItem `json:"results"`
		Truncated      bool            `json:"truncated,omitempty"`
		Note           string          `json:"note,omitempty"`
	}{Today: time.Now().Format("2006-01-02"), RequestedCount: requested}
	if len(items) == 0 {
		out.Results = []webSearchItem{}
		out.Note = "没有匹配结果（或结果质量过低被过滤）。可换个关键词，或去掉 site:/after: 等限定后重试"
	}
	// 总预算：摘要 + 正文合计不超过 maxContentChars；超了就不再追加后续条目。
	budget := t.searchMaxChars
	for _, item := range items {
		cost := utf8.RuneCountInString(item.Summary) + utf8.RuneCountInString(item.Content)
		if len(out.Results) > 0 && budget-cost < 0 {
			out.Truncated = true
			break
		}
		budget -= cost
		out.Results = append(out.Results, item)
	}
	out.ReturnedCount = len(out.Results)
	if out.Truncated {
		// 只给模型真能执行的建议：search_max_chars 是服务端配置，模型拧不到
		out.Note = fmt.Sprintf("结果被服务端预算截断，只返回前 %d/%d 条；如需更多请减少 count 或关闭 need_content 后重试",
			out.ReturnedCount, len(items))
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

		// need_content=true 时上游同时给 summary 和 content：只留 content，避免同一篇正文占两遍预算。
		// content 缺失（个别结果抽不出正文）才回退用 summary。
		item := webSearchItem{Title: title, URL: link}
		if r.Content != "" {
			item.Content = truncateRunes(cleanArticleContent(unescapeMarkdown(r.Content)), t.maxContentChars)
		}
		if item.Content == "" {
			item.Summary = r.Summary
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

// webRecentWindows — recent 参数 → 回溯天数（day=当天）。
var webRecentWindows = map[string]int{
	"day": 0, "week": 7, "month": 30, "year": 365,
}

// appendWebAfter 把 recent 换算成搜索语法 after:YYYY-MM-DD；query 里已有限定就不重复加。
func appendWebAfter(query string, days int) string {
	if strings.Contains(query, "after:") {
		return query
	}
	return strings.TrimSpace(query) + " after:" + time.Now().AddDate(0, 0, -days).Format("2006-01-02")
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
			reason, detail, targetStatus := t.probe(ctx, target)
			ue.Reason, ue.TargetStatus = reason, targetStatus
			// 结论来自目标站/网络就换 stage；私网拦截与非法 URL 归 local
			ue.Stage = "target"
			if reason == "private_address" || reason == "bad_url" {
				ue.Stage = "local"
			}
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
		return t.failResult("page", 0, 0, "error_page",
			fmt.Sprintf("标题「%s」、正文 %d 字符", title, utf8.RuneCountInString(content)), "")
	}

	// 去掉页面外壳（APP 引导、互动计数、推荐位、弹幕表情墙），再按上限截断
	content = cleanArticleContent(content)

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
// 第三个返回值是目标站自己的 HTTP 状态码（拿不到响应时为 0）。
func probeFetchTarget(ctx context.Context, target string) (reason, detail string, targetStatus int) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return "bad_url", "", 0
	}
	ctx, cancel := context.WithTimeout(ctx, webProbeTimeout)
	defer cancel()

	ips, err := net.DefaultResolver.LookupHost(ctx, u.Hostname())
	if err != nil {
		return "dns_error", err.Error(), 0
	}
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil || isBlockedWebIP(ip) {
			// 不让模型把联网工具当内网探测器用
			return "private_address", "解析到受限地址: " + s, 0
		}
	}

	// 用 GET + Range 而不是 HEAD：不少站点对 HEAD 回 405，Range 只取首字节，代价可接受。
	// shortcut: 只校验首跳 DNS 与字面 IP，未防 DNS rebinding；要防需在 Dial 层固定解析结果。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "bad_url", err.Error(), 0
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36")
	req.Header.Set("Range", "bytes=0-0")

	resp, err := (&http.Client{Timeout: webProbeTimeout}).Do(req)
	if err != nil {
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return "timeout", err.Error(), 0
		}
		return "unreachable", err.Error(), 0
	}
	resp.Body.Close()

	reason, detail = classifyTargetResponse(resp.StatusCode, resp.Header.Get("Content-Type"))
	return reason, detail, resp.StatusCode
}

// classifyTargetResponse 把目标站的响应映射成失败原因（纯函数，便于单测）。
// 限流单独归 rate_limited：它该退避重试，而不是换来源。
func classifyTargetResponse(status int, contentType string) (reason, detail string) {
	ct := strings.ToLower(contentType)
	switch {
	case status == http.StatusTooManyRequests:
		return "rate_limited", fmt.Sprintf("本地访问返回 %d（目标站限流，退避后重试）", status)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "blocked", fmt.Sprintf("本地访问返回 %d（反爬/需登录）", status)
	case status == http.StatusNotFound || status == http.StatusGone:
		return "not_found", fmt.Sprintf("本地访问返回 %d", status)
	case status >= 500:
		return "target_error", fmt.Sprintf("本地访问返回 %d", status)
	case isBinaryWebContentType(ct):
		return "unsupported_content", "Content-Type: " + ct
	case status < 400:
		// 我们这边打得通，说明是抓取服务侧的问题（其网络受限或被其策略拦），换来源或稍后重试都可能有效
		return "upstream_only", fmt.Sprintf("本地访问正常（%d），抓取服务侧失败", status)
	default:
		return "target_error", fmt.Sprintf("本地访问返回 %d", status)
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
		return t.failResult(ue.Stage, ue.Status, ue.TargetStatus, ue.Reason, ue.Message, ue.RequestID)
	}
	log.Printf("[web] 未分类失败: %v", err)
	return &Result{Success: false, Error: "请求失败，稍后重试"}
}

// failResult 组装失败结果，并按 stage 决定哪些字段有资格出现：
//   - stage=api    → 只出 api_status（>0 时）
//   - stage=target → 只出 target_status（≥400 时；2xx/3xx/206 一律不露）
//   - stage=page / local → 两个状态码都不出现
func (t *WebTool) failResult(stage string, apiStatus, targetStatus int, reason, detail, requestID string) *Result {
	switch stage {
	case "api":
		targetStatus = 0
	case "target":
		apiStatus = 0
		if targetStatus < 400 {
			targetStatus = 0
		}
	default:
		apiStatus, targetStatus = 0, 0
	}
	data, err := json.Marshal(struct {
		Stage        string `json:"stage"`
		APIStatus    int    `json:"api_status,omitempty"`
		TargetStatus int    `json:"target_status,omitempty"`
		Reason       string `json:"reason"`
		Detail       string `json:"detail,omitempty"`
		RequestID    string `json:"request_id,omitempty"`
	}{Stage: stage, APIStatus: apiStatus, TargetStatus: targetStatus, Reason: reason, Detail: detail, RequestID: requestID})
	if err != nil {
		log.Printf("[web] 失败结果序列化失败: %v", err)
		return &Result{Success: false, Error: webFailMessage(apiStatus, reason, detail, requestID)}
	}
	return &Result{Success: false, Error: webFailMessage(apiStatus, reason, detail, requestID), Data: data}
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
		msg = "触发限流，退避后重试或减少调用次数"
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
		return nil, &webUpstreamError{Stage: "local", Reason: "bad_request", Message: err.Error()}
	}

	status, data, err := t.doOnce(ctx, path, body)
	if err == nil && retryableWebStatus(status) {
		select {
		case <-time.After(t.retryDelay):
		case <-ctx.Done():
			return nil, &webUpstreamError{Stage: "api", Reason: "canceled"}
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
	ue := &webUpstreamError{Stage: "api", Status: status, Reason: webReasonForStatus(status)}
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
	e := &webUpstreamError{Stage: "api", Reason: "network_error", Message: err.Error()}
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

// webBoilerplateHints — 行级页面外壳（APP 引导、客户端提示、运营位）。只对短行生效，避免误删正文。
var webBoilerplateHints = []string{
	"打开APP", "打开 APP", "下载APP", "下载 APP", "下载客户端", "客户端内打开",
	"扫码", "点击查看", "继续阅读", "阅读全文", "查看全文", "即将离开", "回到顶部",
	"分享到", "分享至", "责任编辑", "免责声明", "版权所有",
}

// webEngagementLineRe — 纯互动计数的行，如「评论 0」「点赞 1.2万」。
var webEngagementLineRe = regexp.MustCompile(`^(?:评论|点赞|收藏|分享|转发|参与|阅读|浏览|关注)\s*[\d.,万亿]*\s*$`)

// webRelatedSectionRe — 推荐位起始行，命中后正文到此为止。
var webRelatedSectionRe = regexp.MustCompile(`^(?:相关推荐|热门推荐|猜你喜欢|延伸阅读|相关阅读|推荐阅读)`)

// cleanArticleContent 去掉抓取正文里的页面外壳：
//   - 短行里的 APP 引导 / 客户端提示 / 运营位
//   - 纯互动计数行（评论 0、点赞 1.2万）
//   - 推荐位之后的内容
//   - 弹幕/表情墙（符号占比 >80% 且不短的行）
//   - 连续空行压缩
//
// 保守起见：长行一律不动（宁留噪声，不误删正文）；不做行去重（会毁掉 JSON 的缩进结构）。
func cleanArticleContent(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blanks++
			if blanks > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blanks = 0
		if webRelatedSectionRe.MatchString(trimmed) {
			break
		}
		if isWebBoilerplateLine(trimmed) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isWebBoilerplateLine(line string) bool {
	n := utf8.RuneCountInString(line)
	if n > 40 {
		return false
	}
	for _, h := range webBoilerplateHints {
		if strings.Contains(line, h) {
			return true
		}
	}
	if webEngagementLineRe.MatchString(line) {
		return true
	}
	return n >= 8 && webSymbolRatio(line) > 0.8
}

// webSymbolRatio 统计非空白字符里表情/杂符号的占比（弹幕颜文字墙用）。
func webSymbolRatio(s string) float64 {
	total, symbolic := 0, 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if unicode.Is(unicode.So, r) || (r >= 0x1F000 && r <= 0x1FAFF) ||
			(r >= 0x2600 && r <= 0x27BF) || r == 0xFE0F || r == 0x200D {
			symbolic++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(symbolic) / float64(total)
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
