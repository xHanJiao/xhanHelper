package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"xhan-helper/internal/config"
	"xhan-helper/internal/service"
)

// BochaSearchTool 博查联网搜索工具
type BochaSearchTool struct {
	apiKey         string
	baseURL        string
	totalQuota     int
	quotaExpiresAt time.Time
	usedCount      int
	usageRepo      *service.SearchUsageRepo
	client         *http.Client
}

func NewBochaSearchTool(cfg config.SearchConfig, usageRepo *service.SearchUsageRepo) *BochaSearchTool {
	expiresAt, _ := time.Parse("2006-01-02", cfg.QuotaExpiresAt)
	return &BochaSearchTool{
		apiKey:         cfg.APIKey,
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		totalQuota:     cfg.TotalQuota,
		quotaExpiresAt: expiresAt,
		usedCount:      cfg.UsedCount,
		usageRepo:      usageRepo,
		client:         &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *BochaSearchTool) Name() string { return "search" }

func (t *BochaSearchTool) Description() string {
	return "联网搜索工具，搜索实时信息——新闻、技术文档、百科、金融数据等。当需要最新信息或用户明确要求搜索时调用"
}

func (t *BochaSearchTool) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "search",
			Display:     "博查搜索",
			Description: "联网搜索。返回网页标题、链接、摘要和来源站点",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "搜索关键词",
					},
					"count": map[string]interface{}{
						"type":        "integer",
						"description": "返回结果数，默认 10，最大 50",
					},
					"freshness": map[string]interface{}{
						"type":        "string",
						"description": "时间范围过滤：noLimit(不限)，oneDay(一天内)，oneWeek(一周内)，oneMonth(一月内)，oneYear(一年内)",
						"enum":        []string{"noLimit", "oneDay", "oneWeek", "oneMonth", "oneYear"},
					},
				},
				"required": []interface{}{"query"},
			},
		},
	}
}

func (t *BochaSearchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if _, err := t.usageRepo.TryUse(ec.Ctx, "bocha", t.totalQuota, t.quotaExpiresAt, t.usedCount); err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	var req bochaSearchArgs
	if err := json.Unmarshal(args, &req); err != nil || req.Query == "" {
		return &Result{Success: false, Error: "query is required"}
	}

	results, err := t.callAPI(req)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("搜索失败: %v", err)}
	}

	return &Result{Success: true, Data: results}
}

type bochaSearchArgs struct {
	Query     string `json:"query"`
	Count     int    `json:"count"`
	Freshness string `json:"freshness"`
}

type bochaRequest struct {
	Query     string `json:"query"`
	Freshness string `json:"freshness,omitempty"`
	Summary   bool   `json:"summary"`
	Count     int    `json:"count,omitempty"`
}

type bochaResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		WebPages struct {
			Value []bochaResultItem `json:"value"`
		} `json:"webPages"`
	} `json:"data"`
}

type bochaResultItem struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Snippet       string `json:"snippet"`
	SiteName      string `json:"siteName"`
	DatePublished string `json:"datePublished"`
}

type bochaFormattedItem struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet"`
	SiteName string `json:"siteName"`
}

func (t *BochaSearchTool) callAPI(req bochaSearchArgs) (json.RawMessage, error) {
	body := bochaRequest{
		Query:     req.Query,
		Freshness: req.Freshness,
		Summary:   true,
		Count:     req.Count,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", t.baseURL+"/web-search", bytes.NewReader(bodyBytes))
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

	var sr bochaResponse
	if err := json.Unmarshal(respBytes, &sr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if sr.Code != 200 {
		return nil, fmt.Errorf("API 错误 (code=%d): %s", sr.Code, sr.Message)
	}

	return t.formatResults(sr.Data.WebPages.Value), nil
}

func (t *BochaSearchTool) formatResults(items []bochaResultItem) json.RawMessage {
	formatted := make([]bochaFormattedItem, 0, len(items))
	for _, item := range items {
		formatted = append(formatted, bochaFormattedItem{
			Name:     item.Name,
			URL:      item.URL,
			Snippet:  item.Snippet,
			SiteName: item.SiteName,
		})
	}
	data, _ := json.Marshal(map[string]interface{}{
		"results": formatted,
	})
	return data
}
