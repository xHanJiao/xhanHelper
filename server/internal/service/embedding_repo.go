package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// EmbeddingRepo — 向量数据访问
type EmbeddingRepo struct {
	db *gorm.DB
}

// NewEmbeddingRepo 创建 EmbeddingRepo
func NewEmbeddingRepo(db *gorm.DB) *EmbeddingRepo {
	return &EmbeddingRepo{db: db}
}

// Upsert 创建或更新向量
func (r *EmbeddingRepo) Upsert(ctx context.Context, emb *model.Embedding) error {
	// 先尝试更新，不存在则插入
	var existing model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", emb.EntityType, emb.EntityID).
		First(&existing).Error

	if err == nil {
		// 更新
		emb.ID = existing.ID
		emb.CreatedAt = existing.CreatedAt
		return r.db.WithContext(ctx).Model(&existing).Updates(emb).Error
	}

	// 插入
	return r.db.WithContext(ctx).Create(emb).Error
}

// GetByEntity 获取单个实体的向量
func (r *EmbeddingRepo) GetByEntity(ctx context.Context, entityType, entityID string) (*model.Embedding, error) {
	var emb model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		First(&emb).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &emb, err
}

// ListByType 按类型批量获取向量（用于 warmup）
func (r *EmbeddingRepo) ListByType(ctx context.Context, entityType string, limit, offset int) ([]model.Embedding, error) {
	var list []model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ?", entityType).
		Limit(limit).Offset(offset).
		Find(&list).Error
	return list, err
}

// DeleteByEntity 删除实体的向量
func (r *EmbeddingRepo) DeleteByEntity(ctx context.Context, entityType, entityID string) error {
	return r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Delete(&model.Embedding{}).Error
}
