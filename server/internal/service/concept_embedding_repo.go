package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ConceptEmbeddingRepo — 概念嵌入数据访问
type ConceptEmbeddingRepo struct {
	db *gorm.DB
}

func NewConceptEmbeddingRepo(db *gorm.DB) *ConceptEmbeddingRepo {
	return &ConceptEmbeddingRepo{db: db}
}

// Exists 检查同类型+同值+同用户是否已存在
func (r *ConceptEmbeddingRepo) Exists(ctx context.Context, conceptType, value, userID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ConceptEmbedding{}).
		Where("type = ? AND value = ? AND user_id = ?", conceptType, value, userID).
		Count(&count).Error
	return count > 0, err
}

// Create 创建新记录
func (r *ConceptEmbeddingRepo) Create(ctx context.Context, c *model.ConceptEmbedding) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// ListByType 列出某用户某类型的所有概念（用于聚类）
func (r *ConceptEmbeddingRepo) ListByType(ctx context.Context, conceptType, userID string) ([]model.ConceptEmbedding, error) {
	var list []model.ConceptEmbedding
	err := r.db.WithContext(ctx).
		Where("type = ? AND user_id = ?", conceptType, userID).
		Find(&list).Error
	return list, err
}

// ListByTypeAll 列出指定类型的所有概念（不限用户，用于 warmup）
func (r *ConceptEmbeddingRepo) ListByTypeAll(ctx context.Context, conceptType string) ([]model.ConceptEmbedding, error) {
	var list []model.ConceptEmbedding
	err := r.db.WithContext(ctx).
		Where("type = ?", conceptType).
		Find(&list).Error
	return list, err
}

// ListAllByUser 列出某用户所有类型的概念（topic + tag，用于聚类）
func (r *ConceptEmbeddingRepo) ListAllByUser(ctx context.Context, userID string) ([]model.ConceptEmbedding, error) {
	var list []model.ConceptEmbedding
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Find(&list).Error
	return list, err
}
