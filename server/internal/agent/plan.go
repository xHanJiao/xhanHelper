package agent

import "time"

// Plan — LLM 生成的执行计划
type Plan struct {
	ID      string     `json:"id"`
	Message string     `json:"message"`
	Nodes   []PlanNode `json:"nodes"`
	Edges   []PlanEdge `json:"edges"`
	Status  string     `json:"status"` // running | done | failed
}

// PlanNode — 计划中的一个节点
type PlanNode struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // tool_call | llm | reply
	Label     string                 `json:"label"`
	Status    string                 `json:"status"` // pending | running | done | failed
	Tool      string                 `json:"tool,omitempty"`
	Function  string                 `json:"function,omitempty"`
	Args      map[string]interface{} `json:"args,omitempty"`
	Result    string                 `json:"result,omitempty"`
	DependsOn []string               `json:"depends_on,omitempty"`
	Error     string                 `json:"error,omitempty"`
	StartedAt *time.Time             `json:"started_at,omitempty"`
	EndedAt   *time.Time             `json:"ended_at,omitempty"`
}

// PlanEdge — 节点依赖边
type PlanEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
