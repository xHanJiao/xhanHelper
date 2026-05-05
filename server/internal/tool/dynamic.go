package tool

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DynamicTool — 通过 HTTP 调用 Python sidecar 的动态工具
type DynamicTool struct {
	toolRegistry ToolRegistryRecord
	sidecarURL   string
	httpClient   *http.Client
	cacheReader  CacheReader
	cacheWriter  CacheWriter
	funcSchemas  []FuncSchema
}

// ToolRegistryRecord — 从 DB 加载的一条工具注册记录
type ToolRegistryRecord struct {
	Name        string
	Description string
	SchemaJSON  string // JSON 数组: [{"name":"fn","description":"...","parameters":{...}}]
}

// CacheReader — 缓存读取接口（由 service.ToolRepo 实现）
type CacheReader interface {
	LookupCache(ctx context.Context, toolName, funcName, inputHash string) (cachedOutputJSON string, found bool)
}

// CacheWriter — 缓存写入接口
type CacheWriter interface {
	StoreCache(ctx context.Context, toolName, funcName, inputJSON, inputHash, outputJSON, userID string) error
}

// NewDynamicTool 创建动态工具
func NewDynamicTool(record ToolRegistryRecord, sidecarURL string) *DynamicTool {
	return &DynamicTool{
		toolRegistry: record,
		sidecarURL:   sidecarURL,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// SetCache 注入缓存层
func (t *DynamicTool) SetCache(reader CacheReader, writer CacheWriter) {
	t.cacheReader = reader
	t.cacheWriter = writer
}

func (t *DynamicTool) Name() string        { return t.toolRegistry.Name }
func (t *DynamicTool) Description() string { return t.toolRegistry.Description }

func (t *DynamicTool) Functions() []FuncSchema {
	var funcs []FuncSchema
	if err := json.Unmarshal([]byte(t.toolRegistry.SchemaJSON), &funcs); err != nil {
		return nil
	}
	t.funcSchemas = funcs
	return funcs
}

func (t *DynamicTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	inputJSON := string(args)
	inputHash := hashInput(inputJSON)

	// 查缓存
	if t.cacheReader != nil {
		if cached, ok := t.cacheReader.LookupCache(ec.Ctx, t.toolRegistry.Name, funcName, inputHash); ok {
			var result Result
			if json.Unmarshal([]byte(cached), &result) == nil {
				return &result
			}
		}
	}

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"tool":     t.toolRegistry.Name,
			"function": funcName,
			"args":     json.RawMessage(args),
		},
	}
	b, _ := json.Marshal(body)
	resp, err := t.httpClient.Post(t.sidecarURL+"/mcp", "application/json", bytes.NewReader(b))
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("sidecar unreachable: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		summary := string(respBody)
		if len(summary) > 300 {
			summary = summary[:300]
		}
		return &Result{Success: false, Error: fmt.Sprintf("sidecar status %d: %s", resp.StatusCode, summary)}
	}

	var mcpResult struct {
		Result *Result `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mcpResult); err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("parse sidecar response: %v", err)}
	}
	if mcpResult.Error != nil {
		return &Result{Success: false, Error: mcpResult.Error.Message}
	}
	if mcpResult.Result == nil {
		return &Result{Success: false, Error: "empty sidecar response"}
	}

	// 写缓存（仅标注 cacheable 的函数）
	result := mcpResult.Result
	cacheable := false
	for _, f := range t.funcSchemas {
		if f.Name == funcName {
			cacheable = f.Cacheable
			break
		}
	}
	if result.Success && t.cacheWriter != nil && cacheable {
		outputJSON, _ := json.Marshal(result)
		t.cacheWriter.StoreCache(ec.Ctx, t.toolRegistry.Name, funcName, inputJSON, inputHash, string(outputJSON), ec.UserID)
	}
	return result
}

func hashInput(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}
