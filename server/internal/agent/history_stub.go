package agent

import (
	"encoding/json"
	"log"
	"unicode/utf8"
)

// htmlGetToolName 是 html 工具 get 函数的扁平名（原生 FC 用 tool__func 拼接）。
const htmlGetToolName = "html__get"

const htmlOmittedHint = `整页正文未带入历史（当轮已完整读过）。改文字/换图/加删块先 html.get(view="outline") 拿 nid/hash 再 html.patch（ops / insert / remove）；只有重排版式才用 view="full"`

// StubToolResultForHistory 把「不该原样进历史」的工具结果换成 stub。
// 返回 (内容, 是否替换)。toolName 形如 "html__get"；未知时传 ""。
// maxChars <= 0 表示不按大小兜底（下发就是这么用的）。
//
// 设计见 docs/design/tool-result-history/design.md：宁可不给，也不给被截断的残片。
func StubToolResultForHistory(content, toolName string, maxChars int) (string, bool) {
	if toolName == htmlGetToolName {
		if stub, ok := htmlOmittedStub(content); ok {
			return stub, true
		}
	}
	if maxChars > 0 && utf8.RuneCountInString(content) > maxChars {
		return omittedStub(content, toolName), true
	}
	return content, false
}

// htmlOmittedStub — html.get(full) 的整页正文不看大小一律换掉：patch 依赖新鲜 hash，
// 历史里那份副本在第一次 patch 之后就过期，模型拿它整页重写会静默回滚。
func htmlOmittedStub(content string) (string, bool) {
	var envelope map[string]any
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return "", false
	}
	doc := envelope
	if data, ok := envelope["data"].(map[string]any); ok { // tool.Result 是 {success,data} 信封
		doc = data
	}
	page, ok := doc["html"].(string) // 值不是字符串（null / 对象）不算 html 结果，按大小兜底
	if !ok {
		return "", false
	}

	stub := map[string]any{
		"html_omitted": true,
		"page_bytes":   len(page),
		"hint":         htmlOmittedHint,
	}
	for _, k := range []string{"artifact_id", "title", "summary"} {
		if v, ok := doc[k].(string); ok && v != "" {
			stub[k] = v
		}
	}
	b, err := json.Marshal(stub)
	if err != nil {
		// 退回原文（不是退回截断）：stub 生成失败不该把整条消息变成残片
		log.Printf("[agent] marshal html stub failed: %v", err)
		return content, true
	}
	return string(b), true
}

// omittedStub — 超预算的通用兜底：不再头尾拼接，只说「历史里没带」。
func omittedStub(content, toolName string) string {
	hint := "结果过大，历史中不再携带；需要请重新调用该工具"
	stub := map[string]any{
		"omitted": true,
		"bytes":   len(content),
	}
	if toolName != "" {
		stub["tool"] = toolName
		hint = "结果过大，历史中不再携带；需要请重新调用 " + toolName
	}
	stub["hint"] = hint
	b, err := json.Marshal(stub)
	if err != nil {
		log.Printf("[agent] marshal omitted stub failed: %v", err)
		return content
	}
	return string(b)
}
