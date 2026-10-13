package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// RecordMemoryRunner — 主循环 record_memory 工具的执行接口，由 agent 层实现。
// tool 包不反向依赖 agent：这里只定义窄接口，接线在 appinit 完成。
type RecordMemoryRunner interface {
	// RecordInline 对一条候选跑决策循环（entry=inline），返回 `{action,id,summary}` JSON。
	// ctx 透传主聊天的场景（审计标注用），不能丢。
	RecordInline(ctx context.Context, userID, topic, summary string, facts []string) ([]byte, error)
}

// RecordMemoryInstaller — 延迟接线：工具在 InitTools 建、决策循环在 InitAgents 建，
// 故先建工具、InitAgents 时回填 runner。
type RecordMemoryInstaller interface {
	SetRunner(r RecordMemoryRunner)
}

// RecordMemoryTool — 主循环的写入工具：把「当场记一条」交给决策循环。
// 取代退役的 note.record_note：主 LLM 只表达「我要记这条」，去重/改删由循环判。
type RecordMemoryTool struct {
	runner RecordMemoryRunner
}

func NewRecordMemoryTool(runner RecordMemoryRunner) *RecordMemoryTool {
	return &RecordMemoryTool{runner: runner}
}

// SetRunner 回填决策循环（InitAgents 时调用）。
func (t *RecordMemoryTool) SetRunner(r RecordMemoryRunner) { t.runner = r }

func (t *RecordMemoryTool) Name() string { return "record_memory" }
func (t *RecordMemoryTool) Description() string {
	return "记录长期记忆：当用户分享关于自身的长期信息（决定/结论/洞察/经验/观点/偏好/状态/人物关系）时调用。topic 填一个主题词（一条一个主题）。不记：日常闲聊、本轮任务怎么推进（产出形式/推进顺序/本轮口径）、已写进文档的内容、逐日流水/台账（走 document）。可能与已有记忆重复，会自动新增/更新/删除。"
}

func (t *RecordMemoryTool) Catalog() (CatalogEntry, bool) {
	return CatalogEntry{
		ShortDesc: "记录长期记忆（决定/结论/洞察/偏好/状态/关系），一条一个 topic。日常闲聊、本轮任务过程、逐日流水走 document。",
		Funcs:     []string{"record_memory"},
	}, true
}

func (t *RecordMemoryTool) Functions() []FuncSchema {
	return []FuncSchema{{
		Name:        "record_memory",
		Display:     "记录记忆",
		Description: "保存一条值得长期留存的记忆。当用户分享关于自身的长期信息（决定/结论/经验/观点/偏好/状态/人物关系）时调用。topic 填一个主题词（一条一个主题）。日常闲聊、本轮任务怎么推进、已写进文档的内容、逐日流水/台账不记，走 document。可能触达已有记忆的自动更新或删除。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"topic":   map[string]interface{}{"type": "string", "description": "主题词，非空、≤5 字、只含中英文数字、一条一个主题（会被系统归类收敛，可放心填）"},
				"summary": map[string]interface{}{"type": "string", "description": "一句话概括核心信息，自足成句（主体写进句子）"},
				"facts": map[string]interface{}{
					"type": "array", "items": map[string]interface{}{"type": "string"},
					"description": "支持细节（日期/数字/金额/要点），可选",
				},
			},
			"required": []string{"topic", "summary"},
		},
	}}
}

// ReadOnlyFuncs — nil = 全可写（plan 模式下被拦截）。
func (t *RecordMemoryTool) ReadOnlyFuncs() map[string]bool { return nil }

func (t *RecordMemoryTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
	if funcName != "record_memory" {
		return &Result{Success: false, Error: fmt.Sprintf("unknown function: %s", funcName)}
	}
	var a struct {
		Topic   string   `json:"topic"`
		Summary string   `json:"summary"`
		Facts   []string `json:"facts"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return &Result{Success: false, Error: "参数解析失败: " + err.Error()}
	}
	if a.Summary == "" {
		return &Result{Success: false, Error: "summary 不能为空"}
	}
	data, err := t.runner.RecordInline(ec.Ctx, ec.UserID, a.Topic, a.Summary, a.Facts)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	return &Result{Success: true, Data: data}
}
