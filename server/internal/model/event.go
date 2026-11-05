package model

import "time"

// Event — 事件：一簇记忆的聚合，代表现实里持续存在的一件事。
//
// 事件的「正文」就是挂在它名下的记忆（user_memories.event_id），不另存 Content。
// 没有 active/ended/merged 状态位：
//   - 「结束」靠 last_memory_at 不再更新、注入时自然沉底表达；
//   - 「合并」的实现是把记忆搬走 + 源事件软删，不留别名。
//
// 注意：本表在主库（xhan_helper），与活动埋点库 events.db 里的 events 表**无关**。
// 只读 API 路由是 /api/event-threads（不要用 /api/events，会和埋点接口撞名）。
type Event struct {
	ID     string `gorm:"primaryKey;size:36" json:"id"`
	UserID string `gorm:"size:36;not null;index:idx_events_user_deleted,priority:1" json:"user_id"`
	Name   string `gorm:"size:100;not null" json:"name"`
	// Summary 是「当前状态」的派生视图，≤600 字；完整历史在记忆里。
	Summary string `gorm:"type:text" json:"summary"`
	// IsDeleted 软删除标记。与注入无关（注入走记忆分组，删掉的事件没有记忆），
	// 只影响列表与工具查询。用显式 bool 而非 gorm.DeletedAt：后者会自动过滤所有查询。
	// 对外暴露：list(include_deleted) 与前端需要能区分「已删除」和「不存在」。
	IsDeleted bool `gorm:"default:false;index:idx_events_user_deleted,priority:2" json:"is_deleted"`
	// LastMemoryAt 由 EventRepo 在挂载记忆时自动推进（取该事件记忆的最大 updated_at），
	// 调用方不需要手写。候选查询与列表排序都依赖它。
	LastMemoryAt *time.Time `gorm:"index:idx_events_user_deleted,priority:3" json:"last_memory_at,omitempty"`
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}
