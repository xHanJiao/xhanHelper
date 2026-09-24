package agent

import (
	"encoding/json"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

// recordToolDefinitions — 决策循环的三个 ToolDefinition（函数名由 provider 拼成 `memory__save_memory`）。
func recordToolDefinitions() []llm.ToolDefinition {
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	return []llm.ToolDefinition{
		{
			Name:        recordToolSearch,
			Description: "检索记忆库与旧笔记（只读）",
			Functions: []llm.FuncDef{
				{Name: "search_memory_semantic", Description: "语义检索记忆", Parameters: obj(map[string]interface{}{
					"query": str("自然语言查询"), "top_k": map[string]interface{}{"type": "integer"},
				}, "query")},
				{Name: "search_memory_keyword", Description: "关键词检索记忆（擅长人名/专有名词/日期/金额等字面量）", Parameters: obj(map[string]interface{}{
					"keywords": str("空格或逗号分隔，OR 语义"), "topic": str("可选，按主题过滤"), "top_k": map[string]interface{}{"type": "integer"},
				}, "keywords")},
				{Name: "search_note_semantic", Description: "语义检索旧笔记（过渡期）", Parameters: obj(map[string]interface{}{
					"query": str("自然语言查询"), "top_k": map[string]interface{}{"type": "integer"},
				}, "query")},
				{Name: "search_note_keyword", Description: "关键词检索旧笔记（过渡期）", Parameters: obj(map[string]interface{}{
					"keywords": str("空格或逗号分隔，OR 语义"), "topic": str("可选，按主题过滤"), "top_k": map[string]interface{}{"type": "integer"},
				}, "keywords")},
			},
		},
		{
			Name:        recordToolMemory,
			Description: "对候选记忆执行写入决策",
			Functions: []llm.FuncDef{
				{Name: "save_memory", Description: "新开一条记忆", Parameters: obj(map[string]interface{}{
					"topic": str("一个主题词，≤20 字，禁斜杠/逗号/多主题"), "summary": str("自足成句的一句话，≤150 字"),
					"facts": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "0~10 条细节，每条 ≤100 字"},
				}, "topic", "summary")},
				{Name: "update_memory", Description: "覆盖近邻中某条讲同一件事的记忆（facts 会自动与已有去重合并，只增不减）", Parameters: obj(map[string]interface{}{
					"target_id": str("被覆盖的近邻 id，只能取自输入的 id"), "summary": str("改写后的完整句子"),
					"topic": str("可选，主题变了才传"), "facts": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "新增的细节（会自动与已有去重合并，只增不减）"},
				}, "target_id", "summary")},
				{Name: "delete_memory", Description: "删除近邻中某条被明确否定的记忆", Parameters: obj(map[string]interface{}{
					"target_id": str("被删除的近邻 id，只能取自输入的 id"),
				}, "target_id")},
			},
		},
		{
			Name:        recordToolLegacy,
			Description: "清理旧笔记（过渡期，只删不写）",
			Functions: []llm.FuncDef{
				{Name: "delete_note", Description: "作废一条已被记忆承接的旧笔记", Parameters: obj(map[string]interface{}{
					"target_id": str("被删除的旧笔记 id，只能取自输入的 id"),
				}, "target_id")},
			},
		},
	}
}

// obj 构造一个 JSON Schema object（required 为必填字段名）。
func obj(props map[string]interface{}, required ...string) map[string]interface{} {
	m := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]interface{}, len(required))
		for i, r := range required {
			req[i] = r
		}
		m["required"] = req
	}
	return m
}

func isWriteFunc(fn string) bool {
	switch fn {
	case "save_memory", "update_memory", "delete_memory", "delete_note":
		return true
	}
	return false
}

func writeFuncAction(fn string) string {
	switch fn {
	case "save_memory":
		return "add"
	case "update_memory":
		return "update"
	case "delete_memory", "delete_note":
		return "delete"
	}
	return "none"
}

// extractResultID 从工具返回的 JSON `{action,id}` 里取 id（缺失/坏 JSON 记日志后返回空）。
func extractResultID(r *tool.Result) string {
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.Data, &v); err != nil {
		log.Printf("[record_loop] parse tool result id error: %v (raw %.80s)", err, r.Data)
		return ""
	}
	return v.ID
}
