package agent

import (
	"encoding/json"

	"xhan-helper/internal/tool"
)

// ToolCallLog — 一次工具调用的完整记录（mini_react / record_loop / subagent 共用）。
type ToolCallLog struct {
	Tool     string          `json:"tool"`
	Function string          `json:"function"`
	Args     json.RawMessage `json:"args"`
	Result   *tool.Result    `json:"result"`
}
