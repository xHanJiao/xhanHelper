package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"xhan-helper/internal/config"
)

// dailyQuota 内存中的每日配额计数，Mutex 保护并发安全
type dailyQuota struct {
	mu    sync.Mutex
	date  string // "2006-01-02"
	count int
}

func (q *dailyQuota) tryUse(limit int) (used int, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	today := time.Now().In(config.BJ).Format("2006-01-02")
	if q.date != today {
		q.date = today
		q.count = 0
	}

	if limit > 0 && q.count >= limit {
		return q.count, false
	}
	q.count++
	return q.count, true
}

func (q *dailyQuota) snapshot() map[string]interface{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	today := time.Now().In(config.BJ).Format("2006-01-02")
	if q.date != today {
		return map[string]interface{}{"used": 0, "limit": 0, "date": today}
	}
	return map[string]interface{}{"used": q.count, "limit": 0, "date": q.date}
}

// searchToolInstance 供监控快照读取
var searchToolInstance *SearchTool

// SearchUsageSnapshot 返回当前搜索配额使用情况
func SearchUsageSnapshot() any {
	if searchToolInstance == nil {
		return map[string]interface{}{"used": 0, "limit": 0, "date": ""}
	}
	q := searchToolInstance.quota.snapshot()
	q["limit"] = searchToolInstance.dailyLimit
	return q
}

// SearchTool — 联网搜索工具（支持多 provider）
type SearchTool struct {
	provider   string
	apiKey     string
	baseURL    string
	dailyLimit int
	quota      *dailyQuota
	client     *http.Client
}

func NewSearchTool(cfg config.SearchConfig) *SearchTool {
	st := &SearchTool{
		provider:   cfg.Provider,
		apiKey:     cfg.APIKey,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		dailyLimit: cfg.DailyLimit,
		quota:      &dailyQuota{},
		client:     &http.Client{Timeout: 10 * time.Second},
	}
	searchToolInstance = st
	return st
}

func (t *SearchTool) Name() string { return "search" }

func (t *SearchTool) Description() string {
	return "联网搜索工具，搜索实时信息——新闻、技术文档、百科、金融数据等。当需要最新信息或用户明确要求搜索时调用"
}

func (t *SearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "search",
			Description: "联网搜索。支持按领域、内容类型、语言、区域过滤",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "搜索关键词",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "返回结果数，默认 10，最大 100（建议 5-10）",
					},
					"domain": map[string]interface{}{
						"type":        "string",
						"description": "领域过滤，可选: general, code, tech, fashion, travel, home, ecommerce, gaming, film, music, finance, academic, legal, business, ip, security, education, health, religion, geo, environment, energy",
					},
					"tag": map[string]interface{}{
						"type":        "string",
						"description": "细分标签，格式 {domain}.{sub}，如 code.doc(技术文档)、news.hot(热点新闻)",
					},
					"content_types": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "内容类型过滤，可选值: web, news, doc, image, video。默认 web",
					},
					"zone": map[string]interface{}{
						"type":        "string",
						"description": "区域：cn(国内) 或 intl(国际)",
					},
					"language": map[string]interface{}{
						"type":        "string",
						"description": "语言偏好，如 zh-CN、en。默认与用户对话语言一致",
					},
				},
				"required": []interface{}{"query"},
			},
		},
	}
}

func (t *SearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	used, ok := t.quota.tryUse(t.dailyLimit)
	if !ok {
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("今日搜索次数已用完（%d/%d），请明天再试", used, t.dailyLimit),
		}
	}

	var req searchArgs
	if err := json.Unmarshal(args, &req); err != nil || req.Query == "" {
		return &Result{Success: false, Error: "query is required"}
	}

	results, err := t.callAPI(req)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("搜索失败: %v", err)}
	}

	return &Result{Success: true, Data: results}
}

// searchArgs LLM 传入的搜索参数
type searchArgs struct {
	Query        string   `json:"query"`
	MaxResults   int      `json:"max_results"`
	Domain       string   `json:"domain"`
	Tag          string   `json:"tag"`
	ContentTypes []string `json:"content_types"`
	Zone         string   `json:"zone"`
	Language     string   `json:"language"`
}

// anysearchRequest 发送给 AnySearch API 的请求体
type anysearchRequest struct {
	Query        string   `json:"query"`
	MaxResults   int      `json:"max_results,omitempty"`
	Domain       string   `json:"domain,omitempty"`
	Tag          string   `json:"tag,omitempty"`
	ContentTypes []string `json:"content_types,omitempty"`
	Zone         string   `json:"zone,omitempty"`
	Language     string   `json:"language,omitempty"`
}

// anysearchResponse AnySearch API 响应
type anysearchResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
			Content string `json:"content"`
		} `json:"results"`
	} `json:"data"`
}

func (t *SearchTool) callAPI(req searchArgs) (json.RawMessage, error) {
	body := anysearchRequest{
		Query:        req.Query,
		MaxResults:   req.MaxResults,
		Domain:       req.Domain,
		Tag:          req.Tag,
		ContentTypes: req.ContentTypes,
		Zone:         req.Zone,
		Language:     req.Language,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", t.baseURL+"/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+t.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("状态码 %d: %s", resp.StatusCode, string(respBytes))
	}

	var sr anysearchResponse
	if err := json.Unmarshal(respBytes, &sr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if sr.Code != 0 {
		return nil, fmt.Errorf("API 错误 (code=%d): %s", sr.Code, sr.Message)
	}

	return formatResults(sr.Data.Results), nil
}

// searchResultItem 格式化给 LLM 的搜索结果项（保留 title + snippet + content，去除 url）
type searchResultItem struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Content string `json:"content,omitempty"`
}

func formatResults(results []struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Content string `json:"content"`
}) json.RawMessage {
	items := make([]searchResultItem, 0, len(results))
	for _, r := range results {
		items = append(items, searchResultItem{
			Title:   r.Title,
			Snippet: r.Snippet,
			Content: r.Content,
		})
	}
	data, _ := json.Marshal(map[string]interface{}{
		"results": items,
	})
	return data
}
