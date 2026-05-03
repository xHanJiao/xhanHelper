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

// Delete 删除笔记
func (r *NoteRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.Note{}, "id = ?", id).Error
}

// List 按条件查询
func (r *NoteRepo) List(ctx context.Context, userID, topic, keyword string) ([]model.Note, error) {
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if topic != "" {
		q = q.Where("topic = ?", topic)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("content LIKE ? OR tags LIKE ?", like, like)
	}
	var list []model.Note
	err := q.Order("created_at DESC").Find(&list).Error
	return list, err
}

// ListByUser 获取设备所有笔记
func (r *NoteRepo) ListByUser(ctx context.Context, userID string) ([]model.Note, error) {
	return r.List(ctx, userID, "", "")
}
