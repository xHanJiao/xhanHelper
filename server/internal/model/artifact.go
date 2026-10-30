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
	ID             string `gorm:"primaryKey;size:36" json:"id"`
	UserID         string `gorm:"size:36;not null;index" json:"-"`
	ConversationID string `gorm:"size:36;not null;index" json:"conversation_id"`
	Title          string `gorm:"size:200;not null" json:"title"`
	Summary        string `gorm:"size:500;not null" json:"summary"`
	HTML           string `gorm:"type:longtext;not null" json:"-"`
	// SourceHTML — 模型给的原文（清洗前、图片仍是 `image:<id>` 引用）。
	// 只给 `html.get` 读回用：模型要改版时得拿到整页 HTML，而 HTML 列里图片已内联成
	// 几 MB 的 data: URI，塞进上下文既不现实也浪费（见 design §4.2 / §8.5）。
	//
	// **不要加 not null**：老库升级走 ALTER TABLE ADD COLUMN，SQLite 不接受
	// 「NOT NULL 且无默认值」的新列（Cannot add a NOT NULL column with default value NULL），
	// 本地版会直接起不来。旧行读出来是 NULL，GORM 落到 string 就是 ""，
	// `get` 已经把空串当「没有原文」处理。
	SourceHTML string `gorm:"type:longtext" json:"-"`
	// ShareToken — 公开分享链接的凭证（空 = 未分享）。1:1、永久、可撤销（见 design §9.6）。
	ShareToken string         `gorm:"size:64;index" json:"-"`
	CreatedAt  time.Time      `gorm:"autoCreateTime" json:"created_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
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
