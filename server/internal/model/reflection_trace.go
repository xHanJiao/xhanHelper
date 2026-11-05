package model

import "time"

// ReflectionTrace — 一次反思循环的完整记录
type ReflectionTrace struct {
	ID             string     `gorm:"primaryKey;size:36" json:"id"`
	UserID         string     `gorm:"size:36;not null;index" json:"user_id"`
	TriggerType    string     `gorm:"size:20;not null" json:"trigger_type"` // cron | event
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	Status         string     `gorm:"size:20;default:running" json:"status"` // running | completed | empty | error
	TotalFindings  int        `json:"total_findings"`
	DeliveredCount int        `json:"delivered_count"`
	ErrorMessage   *string    `gorm:"type:text" json:"error_message,omitempty"`
}

// ReflectionLLMCall — 反思循环中的一次 LLM 调用
type ReflectionLLMCall struct {
	ID               string    `gorm:"primaryKey;size:36" json:"id"`
	TraceID          string    `gorm:"size:36;not null;index" json:"trace_id"`
	Caller           string    `gorm:"size:50;not null" json:"caller"`          // 调用方（如 MissionRunner）
	CallerDetail     string    `gorm:"size:100" json:"caller_detail,omitempty"` // 如 "MissionRunner:职业规划"
	RequestJSON      *string   `gorm:"type:longtext" json:"request_json,omitempty"`
	ResponseJSON     *string   `gorm:"type:longtext" json:"response_json,omitempty"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	LatencyMs        int64     `json:"latency_ms"`
	ErrorMessage     *string   `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt        time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// ReflectionDecision — 反思循环中一个关键决策
type ReflectionDecision struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	TraceID    string    `gorm:"size:36;not null;index" json:"trace_id"`
	Stage      string    `gorm:"size:30;not null" json:"stage"`    // tool_execution | finding | discovery
	Subject    string    `gorm:"size:100;not null" json:"subject"` // 如 "职业规划" | "finding f-003"
	Verdict    string    `gorm:"size:20;not null" json:"verdict"`  // selected | skipped | topn | dropped | rejected | nominated
	Reason     string    `gorm:"type:text;not null" json:"reason"`
	DetailJSON *string   `gorm:"type:json" json:"detail_json,omitempty"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
