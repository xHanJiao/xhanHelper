package service

import (
	"context"
	"time"

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

// ListByTypeSince 按类型 + 创建时间下界批量获取向量（用于按保留窗 warmup）
func (r *EmbeddingRepo) ListByTypeSince(ctx context.Context, entityType string, since time.Time, limit, offset int) ([]model.Embedding, error) {
	var list []model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND created_at >= ?", entityType, since).
		Limit(limit).Offset(offset).
		Find(&list).Error
	return list, err
}

// ListEntityIDsByTypeBefore 列出某类型在 before 之前创建的 entity_id（逐出内存向量用；不删 DB 行）
func (r *EmbeddingRepo) ListEntityIDsByTypeBefore(ctx context.Context, entityType string, before time.Time) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).Model(&model.Embedding{}).
		Where("entity_type = ? AND created_at < ?", entityType, before).
		Pluck("entity_id", &ids).Error
	return ids, err
}

// ListByEntities 按类型 + 一组 entityID 批量获取向量（供 get_topic_stats 等避免 N+1）
func (r *EmbeddingRepo) ListByEntities(ctx context.Context, entityType string, entityIDs []string) ([]model.Embedding, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	var list []model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id IN ?", entityType, entityIDs).
		Find(&list).Error
	return list, err
}

// DeleteByType 删除某类型全部向量行（一次性清理 mission 等）
func (r *EmbeddingRepo) DeleteByType(ctx context.Context, entityType string) (int64, error) {
	result := r.db.WithContext(ctx).Where("entity_type = ?", entityType).Delete(&model.Embedding{})
	return result.RowsAffected, result.Error
}

// DeleteByEntities 按类型 + 一组 entityID 批量删除向量行
func (r *EmbeddingRepo) DeleteByEntities(ctx context.Context, entityType string, entityIDs []string) (int64, error) {
	if len(entityIDs) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id IN ?", entityType, entityIDs).
		Delete(&model.Embedding{})
	return result.RowsAffected, result.Error
}

// DeleteChunksByParentType 删除某父类型的全部 chunk 向量行（entity_id 前缀 chunk:<parentType>:）
func (r *EmbeddingRepo) DeleteChunksByParentType(ctx context.Context, parentType string) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id LIKE ?", "chunk", "chunk:"+parentType+":%").
		Delete(&model.Embedding{})
	return result.RowsAffected, result.Error
}

// BatchUpsert 批量 upsert 向量
func (r *EmbeddingRepo) BatchUpsert(ctx context.Context, embeddings []model.Embedding) error {
	if len(embeddings) == 0 {
		return nil
	}
	// 逐个 upsert（GORM 不支持 ON DUPLICATE KEY UPDATE 的批量操作）
	for i := range embeddings {
		if err := r.Upsert(ctx, &embeddings[i]); err != nil {
			return err
		}
	}
	return nil
}

// DeleteByEntity 删除实体的向量
func (r *EmbeddingRepo) DeleteByEntity(ctx context.Context, entityType, entityID string) error {
	return r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Delete(&model.Embedding{}).Error
}
