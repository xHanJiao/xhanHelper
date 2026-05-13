package model

import "time"

// MCPServerRecord — MCP server 工具快照记录
type MCPServerRecord struct {
	ID                string    `gorm:"primaryKey;size:36" json:"id"`
	Name              string    `gorm:"uniqueIndex;size:100;not null" json:"name"`
	ToolsSnapshot     string    `gorm:"type:text" json:"tools_snapshot"` // JSON: [{name, description, inputSchema}]
	SnapshotUpdatedAt time.Time `json:"snapshot_updated_at"`
	Enabled           bool      `gorm:"default:true" json:"enabled"`
	CreatedAt         time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
