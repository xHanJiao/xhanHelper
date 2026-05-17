package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// ==================== 策略接口 ====================

// ToolRetrieveStrategy 工具检索策略接口
type ToolRetrieveStrategy interface {
	// Name 策略名称
	Name() string
	// Search 搜索相关工具
	Search(ctx context.Context, query string, limit int) ([]ToolInfo, error)
	// SearchHint 告诉 LLM 如何高效搜索（会出现在 tool_retriever 的描述中）
	SearchHint() string
}

// ToolInfo 工具信息（用于返回给 LLM）
type ToolInfo struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Category    string             `json:"category"`
	Functions   []ToolFunctionInfo `json:"functions"`
	Score       float64            `json:"score,omitempty"`
}

// ToolFunctionInfo 工具函数信息
type ToolFunctionInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ==================== 策略持有者 ====================

// RegistrySearcher — 在 Registry 内存工具中搜索的接口
type RegistrySearcher interface {
	SearchTools(query string) []ToolInfo
}

// ToolRetriever 工具检索工具（支持策略切换 + Registry 内存搜索）
type ToolRetriever struct {
	strategy    ToolRetrieveStrategy
	regSearcher RegistrySearcher // nil 时跳过内存搜索
}

// NewToolRetriever 创建 ToolRetriever
func NewToolRetriever(strategy ToolRetrieveStrategy) *ToolRetriever {
	return &ToolRetriever{
		strategy: strategy,
	}
}

// SetRegistrySearcher 注入 Registry 内存搜索能力
func (t *ToolRetriever) SetRegistrySearcher(rs RegistrySearcher) {
	t.regSearcher = rs
}

// SetStrategy 切换策略
func (t *ToolRetriever) SetStrategy(strategy ToolRetrieveStrategy) {
	log.Printf("[ToolRetriever] switch strategy: %s -> %s", t.strategy.Name(), strategy.Name())
	t.strategy = strategy
}

// Strategy 获取当前策略
func (t *ToolRetriever) Strategy() ToolRetrieveStrategy {
	return t.strategy
}

// ==================== Tool 接口实现 ====================

func (t *ToolRetriever) Name() string {
	return "tool_retriever"
}

func (t *ToolRetriever) Description() string {
	return fmt.Sprintf("检索可用工具。%s", t.strategy.SearchHint())
}

func (t *ToolRetriever) Functions() []FuncSchema {
	return []FuncSchema{
		{
			Name:        "search",
			Description: "搜索相关工具",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "工具名或相关关键词，如 'bookkeeping'、'记账'",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "最多返回多少个工具，默认 5",
						"default":     5,
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

func (t *ToolRetriever) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName != "search" {
		return &Result{Success: false, Error: "unknown function: " + funcName}
	}

	var req struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return &Result{Success: false, Error: "invalid args: " + err.Error()}
	}

	if req.Limit <= 0 {
		req.Limit = 5
	}

	log.Printf("[ToolRetriever] search: strategy=%s query=%q limit=%d",
		t.strategy.Name(), req.Query, req.Limit)

	// Registry 内存搜索结果优先（有完整函数定义）
	// DB 策略结果补充（动态工具等），按 name 去重
	seen := make(map[string]bool)
	var tools []ToolInfo

	if t.regSearcher != nil {
		for _, ti := range t.regSearcher.SearchTools(req.Query) {
			if !seen[ti.Name] {
				seen[ti.Name] = true
				tools = append(tools, ti)
			}
		}
	}

	strategyTools, err := t.strategy.Search(ec.Ctx, req.Query, req.Limit)
	if err != nil {
		log.Printf("[ToolRetriever] search error: %v", err)
		return &Result{Success: false, Error: err.Error()}
	}
	for _, ti := range strategyTools {
		if !seen[ti.Name] && len(tools) < req.Limit {
			seen[ti.Name] = true
			tools = append(tools, ti)
		}
	}

	resJSON, _ := json.Marshal(map[string]interface{}{
		"tools":    tools,
		"strategy": t.strategy.Name(),
		"query":    req.Query,
	})

	return &Result{Success: true, Data: resJSON}
}
