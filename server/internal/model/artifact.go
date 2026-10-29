package model

import (
	"time"

	"gorm.io/gorm"
)

// Artifact — 聊天里由工具生成、需要独立页面展示的 HTML 产物。
//
// 正文只存在这里；消息表只存 id（见 docs/design/html-artifact/design.md）。
// HTML 字段打了 `json:"-"`，任何直接序列化都不会把正文漏给列表接口——
// 列表只能通过 ArtifactRef / FindRefsByIDs 取标题和摘要。
type Artifact struct {
	ID             string         `gorm:"primaryKey;size:36" json:"id"`
	UserID         string         `gorm:"size:36;not null;index" json:"user_id"`
	ConversationID string         `gorm:"size:36;not null;index" json:"conversation_id"`
	Title          string         `gorm:"size:200;not null" json:"title"`
	Summary        string         `gorm:"size:500;not null" json:"summary"`
	HTML           string         `gorm:"type:longtext;not null" json:"-"`
	CreatedAt      time.Time      `gorm:"autoCreateTime" json:"created_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

// ArtifactRef — 消息列表里用来渲染卡片的轻量引用（不含正文）。
type ArtifactRef struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// ArtifactTypeHTML — 目前唯一的产物类型。留字段是为了以后能加别的渲染器。
const ArtifactTypeHTML = "html"
