package model

import (
	"fmt"
	"strings"
	"time"
)

// EntityType 实体类型（同时用作 embeddings.entity_type 与 chunk 的 parent_type）
const (
	EntityTypeMessage = "message"
	EntityTypeNote    = "note"
	EntityTypeMemory  = "memory"
	EntityTypeTool    = "tool"
	EntityTypeTheme   = "theme"
	EntityTypeConcept = "concept"
	EntityTypeSummary = "summary"
	EntityTypeChunk   = "chunk"
)

// 内存向量库 metadata["type"] 取值（不带冒号）。
// 注意与 embeddings.entity_type 的差异：memory 的向量 type 是 "mem"（非 "memory"），
// message 是 "msg"（非 "message"）。这是「向量库词表」，与 entity_type 那套分开维护。
const (
	VectorTypeNote    = "note"
	VectorTypeMemory  = "mem"
	VectorTypeChunk   = "chunk"
	VectorTypeMessage = "msg"
	VectorTypeSummary = "summary"
	VectorTypeTheme   = "theme"
	VectorTypeTool    = "tool"
)

// 内存向量库 key 前缀 = 对应 type + ":"（同一套向量库词表）。
// 写入 / 检索 / 清理必须引用同一套，禁止再写字面量。
const (
	VectorPrefixNote    = VectorTypeNote + ":"
	VectorPrefixMemory  = VectorTypeMemory + ":"
	VectorPrefixChunk   = VectorTypeChunk + ":"
	VectorPrefixMessage = VectorTypeMessage + ":"
	VectorPrefixSummary = VectorTypeSummary + ":"
	VectorPrefixTheme   = VectorTypeTheme + ":"
	VectorPrefixTool    = VectorTypeTool + ":"
)

// FullVectorPrefix 返回实体全文向量 key 的前缀；entityType ∈ {note, memory}。
func FullVectorPrefix(entityType string) (string, bool) {
	switch entityType {
	case EntityTypeNote:
		return VectorPrefixNote, true
	case EntityTypeMemory:
		return VectorPrefixMemory, true
	}
	return "", false
}

// ChunkVectorKey 构造句级 chunk 的内存向量 key："chunk:<parentType>:<parentID>/<idx>"。
func ChunkVectorKey(parentType, parentID string, idx int) string {
	return fmt.Sprintf("%s%s:%s/%d", VectorPrefixChunk, parentType, parentID, idx)
}

// ChunkVectorKeyPrefix 返回某 parentType 全部 chunk key 的前缀："chunk:<parentType>:"。
func ChunkVectorKeyPrefix(parentType string) string {
	return VectorPrefixChunk + parentType + ":"
}

// ParseChunkVectorKey 反解 chunk key，返回 parentType 与 parentID（与 ChunkVectorKey 互逆）。
func ParseChunkVectorKey(key string) (parentType, parentID string) {
	rest := strings.TrimPrefix(key, VectorPrefixChunk)
	pt, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return "", ""
	}
	if i := strings.LastIndex(rest, "/"); i > 0 {
		rest = rest[:i]
	}
	return pt, rest
}

// Embedding — 向量存储
type Embedding struct {
	ID         string `gorm:"primaryKey;size:100" json:"id"`
	EntityType string `gorm:"uniqueIndex:idx_entity,priority:1;size:20;not null" json:"entity_type"`
	EntityID   string `gorm:"uniqueIndex:idx_entity,priority:2;size:100;not null" json:"entity_id"`
	UserID     string `gorm:"index:idx_user_type,priority:1;size:36;not null" json:"user_id"`
	Model      string `gorm:"size:100;not null" json:"model"`
	// TODO 增加把嵌入之前的内容也保存进来以便追查
	Embedding *string   `gorm:"type:text" json:"-"` // JSON 数组向量数据
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
