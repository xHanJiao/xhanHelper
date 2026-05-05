package model

import "time"

// ToolRegistry — 动态工具注册表
type ToolRegistry struct {
	ID          string    `gorm:"primaryKey;size:36" json:"id"`
	Name        string    `gorm:"size:100;not null" json:"name"`
	Description string    `gorm:"type:text;not null" json:"description"`
	SchemaJSON  string    `gorm:"type:json;not null;column:schema_json" json:"schema_json"`
	ScriptPath  string    `gorm:"size:500;not null;default:''" json:"script_path"`
	Status      string    `gorm:"size:20;not null;default:draft" json:"status"`
	UserID      string    `gorm:"size:36;not null" json:"user_id"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// ToolData — 工具调用记录（支持 memoization 缓存）
type ToolData struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	ToolName   string    `gorm:"size:100;not null" json:"tool_name"`
	FuncName   string    `gorm:"size:100;not null" json:"func_name"`
	InputJSON  string    `gorm:"type:json;not null;column:input_json" json:"input_json"`
	InputHash  string    `gorm:"size:64;not null;column:input_hash" json:"input_hash"`
	OutputJSON string    `gorm:"type:json;not null;column:output_json" json:"output_json"`
	UserID     string    `gorm:"size:36;not null" json:"user_id"`
	Cached     bool      `gorm:"default:false" json:"cached"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}
