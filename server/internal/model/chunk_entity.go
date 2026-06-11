package model

import "time"

// ChunkEntity is satisfied by models that can be chunk-searched and used as
// retrieval context (UserMemory, Note).
type ChunkEntity interface {
	GetID() string
	GetUserID() string
	GetContent() string
	GetCreatedAt() time.Time
	GetTopic() string
}
