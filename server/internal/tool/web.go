package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/config"
)

const (
	// webQuotaProvider — 每日额度的计费主体名。expires_at 设为明日零点，靠 SearchUsageRepo
	// 「expires_at 变化即清零」的机制实现按天滚动（见 service/search_usage_repo.go）。
	webQuotaProvider = "deepseek_web"
	webMaxCount      = 20  // 模型可选条数上限（API 上限 50，此处按 token 预算收紧）
	webMaxQueryRunes = 128 // API 硬限制
	webMaxRespBytes  = 8 << 20
	webRetryDelay    = 800 * time.Millisecond
)

// WebQuota — 每日调用额度的最小依赖。生产实现 = service.SearchUsageRepo。
type WebQuota interface {
	TryUse(ctx context.Context, provider string, totalQuota int, expiresAt time.Time, initialUsed int) (int, error)
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
				"query 支持搜索语法：site:github.com、-site:example.com 排除站点、-词 排除关键词、\"精确短语\"、lang:zh,en、after:2025-01-01、before:2026-06-01。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "搜索词（≤128 字符），可用 site:/after:/before:/lang:/引号等语法收窄范围",
					},
					"count": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("返回条数，默认 %d，最多 %d", t.searchCount, webMaxCount),
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
			Description: "抓取单个网页正文，返回 Markdown。适合官方文档/文章/百科等静态页；JS 渲染页、登录墙会抓不到（报错会说明）。结果会截断到配置上限。",
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
		return &Result{Success: false, Error: err.Error()}
	}

	var resp struct {
		Results []struct {
			Title    string  `json:"title"`
			URL      string  `json:"url"`
			Summary  string  `json:"summary"`
			Content  string  `json:"content"`
			SiteName *string `json:"site_name"`
			Date     *string `json:"date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		log.Printf("[web] search 响应解析失败: %v", err)
		return &Result{Success: false, Error: "搜索服务返回了无法解析的内容"}
	}

	out := struct {
		Results   []webSearchItem `json:"results"`
		Truncated bool            `json:"truncated,omitempty"`
		Note      string          `json:"note,omitempty"`
	}{}
	if len(resp.Results) == 0 {
		out.Results = []webSearchItem{}
		out.Note = "没有匹配结果。可换个关键词，或去掉 site:/after: 等限定后重试"
	}
	// 总预算：摘要 + 正文合计不超过 maxContentChars；超了就不再追加后续条目。
	budget := t.maxContentChars
	for _, r := range resp.Results {
		content := r.Content
		if content != "" {
			content = truncateRunes(content, t.maxContentChars)
		}
		item := webSearchItem{Title: r.Title, URL: r.URL, Summary: r.Summary, Content: content}
		if r.SiteName != nil {
			item.SiteName = *r.SiteName
		}
		if r.Date != nil {
			item.Date = *r.Date
		}
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
		return &Result{Success: false, Error: err.Error()}
	}

	var resp struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		log.Printf("[web] fetch 响应解析失败: %v", err)
		return &Result{Success: false, Error: "抓取服务返回了无法解析的内容"}
	}

	truncated := utf8.RuneCountInString(resp.Content) > t.maxContentChars
	content := truncateRunes(resp.Content, t.maxContentChars)
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
	}{URL: target, Title: resp.Title, Content: content, Truncated: truncated, Note: note})
	if err != nil {
		log.Printf("[web] fetch 结果序列化失败: %v", err)
		return &Result{Success: false, Error: "抓取结果序列化失败"}
	}
	return &Result{Success: true, Data: data}
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
		log.Printf("[web] 额度校验失败: %v", err)
		return fmt.Sprintf("额度校验失败，本次联网未执行: %v", err)
	}
	return ""
}

// ---------- HTTP ----------

// post 发送 JSON 请求，返回响应体。对 408/429/5xx 自动重试一次（官方建议）。
// 返回的 error 文案面向模型，技术细节已就地打日志。
func (t *WebTool) post(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[web] 请求序列化失败: %v", err)
		return nil, fmt.Errorf("请求参数序列化失败")
	}

	status, data, err := t.doOnce(ctx, path, body)
	if err == nil && retryableWebStatus(status) {
		select {
		case <-time.After(t.retryDelay):
		case <-ctx.Done():
			return nil, fmt.Errorf("请求已取消")
		}
		status, data, err = t.doOnce(ctx, path, body)
	}
	if err != nil {
		log.Printf("[web] %s 请求失败: %v", path, err)
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	if status != http.StatusOK {
		log.Printf("[web] %s HTTP %d: %s", path, status, strings.TrimSpace(string(data)))
		return nil, fmt.Errorf("%s", webStatusMessage(status, data))
	}
	return data, nil
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

// webStatusMessage 把 HTTP 状态映射成模型能据此行动的文案。
func webStatusMessage(status int, body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e) // 错误体解析失败不影响下面的兜底文案
	switch status {
	case http.StatusBadRequest:
		if e.Error.Message != "" {
			return fmt.Sprintf("请求不合法（400）: %s", e.Error.Message)
		}
		return "请求不合法（400）"
	case http.StatusUnauthorized:
		return "DeepSeek API Key 无效（401），联网不可用"
	case http.StatusPaymentRequired:
		return "DeepSeek 余额不足（402），联网不可用"
	case http.StatusForbidden:
		return "抓取被安全策略拒绝（403），换其他来源"
	case http.StatusNotFound:
		return "抓不到网页正文（404）：可能是 JS 渲染页面、登录墙或站点拒绝访问；可派 task 子任务 web_explore 用浏览器打开"
	case http.StatusRequestTimeout:
		return "请求超时（408），稍后重试"
	case http.StatusTooManyRequests:
		return "触发限速（429），稍后再试或减少调用次数"
	default:
		if status >= 500 {
			return fmt.Sprintf("联网服务暂时不可用（%d），稍后重试", status)
		}
		return fmt.Sprintf("联网请求失败（%d）", status)
	}
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
