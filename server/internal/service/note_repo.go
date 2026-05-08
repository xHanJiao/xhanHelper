package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// NoteRepo — 笔记数据访问
type NoteRepo struct {
	db *gorm.DB
}

// NewNoteRepo 创建 NoteRepo
func NewNoteRepo(db *gorm.DB) *NoteRepo {
	return &NoteRepo{db: db}
}

// Create 创建笔记
func (r *NoteRepo) Create(ctx context.Context, n *model.Note) error {
	return r.db.WithContext(ctx).Create(n).Error
}

// GetByIDs 按 ID 列表查询
func (r *NoteRepo) GetByIDs(ctx context.Context, ids []string) ([]model.Note, error) {
	var list []model.Note
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// UpdateEmbedding 更新笔记的向量字段
func (r *NoteRepo) UpdateEmbedding(ctx context.Context, id string, embedding *string) error {
	return r.db.WithContext(ctx).Model(&model.Note{}).Where("id = ?", id).
		Update("embedding", embedding).Error
}

// ListAllPaginated 分页获取所有笔记（用于 warmup），limit<=0 时默认 100
func (r *NoteRepo) ListAllPaginated(ctx context.Context, limit, offset int) ([]model.Note, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []model.Note
	err := r.db.WithContext(ctx).Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}

// Delete 删除笔记
func (r *NoteRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.Note{}, "id = ?", id).Error
}

// List 按条件分页查询，limit<=0 时默认 50
func (r *NoteRepo) List(ctx context.Context, userID, topic, keyword string, limit, offset int) ([]model.Note, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if topic != "" {
		q = q.Where("topic = ?", topic)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("content LIKE ? OR tags LIKE ?", like, like)
	}
	var list []model.Note
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}
