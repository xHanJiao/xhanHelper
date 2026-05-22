package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// DocumentRepo — 文档数据访问
type DocumentRepo struct {
	db *gorm.DB
}

func NewDocumentRepo(db *gorm.DB) *DocumentRepo {
	return &DocumentRepo{db: db}
}

// Create 创建文档
func (r *DocumentRepo) Create(ctx context.Context, d *model.Document) error {
	return r.db.WithContext(ctx).Create(d).Error
}

// GetByID 按 ID + 用户 ID 查询单篇
func (r *DocumentRepo) GetByID(ctx context.Context, id, userID string) (*model.Document, error) {
	var doc model.Document
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// Update 部分更新文档
func (r *DocumentRepo) Update(ctx context.Context, id, userID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Document{}).Where("id = ? AND user_id = ?", id, userID).Updates(updates).Error
}

// Delete 删除文档
func (r *DocumentRepo) Delete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Delete(&model.Document{}, "id = ? AND user_id = ?", id, userID).Error
}

// List 按条件分页查询，limit<=0 时默认 50
func (r *DocumentRepo) List(ctx context.Context, userID, tag, keyword string, limit, offset int) ([]model.Document, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if tag != "" {
		q = q.Where("tags LIKE ?", "%"+tag+"%")
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("title LIKE ? OR content LIKE ?", like, like)
	}

	var total int64
	if err := q.Model(&model.Document{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.Document
	err := q.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}
