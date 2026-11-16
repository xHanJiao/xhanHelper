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

// GetByValue 按类型+值+用户获取概念嵌入记录
func (r *ConceptEmbeddingRepo) GetByValue(ctx context.Context, conceptType, value, userID string) (*model.ConceptEmbedding, error) {
	var c model.ConceptEmbedding
	err := r.db.WithContext(ctx).
		Where("type = ? AND value = ? AND user_id = ?", conceptType, value, userID).
		First(&c).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &c, err
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

// DeleteByType 删除某类概念并返回被删 ID（供同步清理对应 embedding 行）
func (r *ConceptEmbeddingRepo) DeleteByType(ctx context.Context, conceptType string) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).Model(&model.ConceptEmbedding{}).
		Where("type = ?", conceptType).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := r.db.WithContext(ctx).Where("type = ?", conceptType).
		Delete(&model.ConceptEmbedding{}).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteByValues 删除某用户某类型下一批 value 的概念行，返回被删 ID（供同步清理对应 embedding 行）。
func (r *ConceptEmbeddingRepo) DeleteByValues(ctx context.Context, conceptType, userID string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	var ids []string
	if err := r.db.WithContext(ctx).Model(&model.ConceptEmbedding{}).
		Where("type = ? AND user_id = ? AND value IN ?", conceptType, userID, values).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := r.db.WithContext(ctx).
		Where("type = ? AND user_id = ? AND value IN ?", conceptType, userID, values).
		Delete(&model.ConceptEmbedding{}).Error; err != nil {
		return nil, err
	}
	return ids, nil
}
