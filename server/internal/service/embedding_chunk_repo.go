package service

import (
	"context"
	"log"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// EmbeddingChunkRepo — chunk 数据访问
type EmbeddingChunkRepo struct {
	db *gorm.DB
}

func NewEmbeddingChunkRepo(db *gorm.DB) *EmbeddingChunkRepo {
	return &EmbeddingChunkRepo{db: db}
}

// Create 写入一条 chunk
func (r *EmbeddingChunkRepo) Create(ctx context.Context, c *model.EmbeddingChunk) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// BatchCreate 批量写入
func (r *EmbeddingChunkRepo) BatchCreate(ctx context.Context, chunks []model.EmbeddingChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(chunks, 100).Error
}

// ListByParent 按 parent 获取所有 chunk
func (r *EmbeddingChunkRepo) ListByParent(ctx context.Context, parentType, parentID string) ([]model.EmbeddingChunk, error) {
	var list []model.EmbeddingChunk
	err := r.db.WithContext(ctx).
		Where("parent_type = ? AND parent_id = ?", parentType, parentID).
		Order("chunk_index ASC").
		Find(&list).Error
	return list, err
}

// BatchGetByParentIDs 批量按 parentIDs 获取 chunks（用于检索后去重）
func (r *EmbeddingChunkRepo) BatchGetByParentIDs(ctx context.Context, parentType string, parentIDs []string) ([]model.EmbeddingChunk, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}
	var list []model.EmbeddingChunk
	err := r.db.WithContext(ctx).
		Where("parent_type = ? AND parent_id IN ?", parentType, parentIDs).
		Order("chunk_index ASC").
		Find(&list).Error
	return list, err
}

// DeleteByParent 删除一个 parent 的所有 chunk（重跑前清理）
func (r *EmbeddingChunkRepo) DeleteByParent(ctx context.Context, parentType, parentID string) error {
	return r.db.WithContext(ctx).
		Where("parent_type = ? AND parent_id = ?", parentType, parentID).
		Delete(&model.EmbeddingChunk{}).Error
}

// DeleteByParents 批量删除多个 parent 的 chunk（entity 硬删等批量场景）。
func (r *EmbeddingChunkRepo) DeleteByParents(ctx context.Context, parentType string, parentIDs []string) (int64, error) {
	if len(parentIDs) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Where("parent_type = ? AND parent_id IN ?", parentType, parentIDs).
		Delete(&model.EmbeddingChunk{})
	return res.RowsAffected, res.Error
}

// DeleteAllByType 删除某类型全部 chunk
func (r *EmbeddingChunkRepo) DeleteAllByType(ctx context.Context, parentType string) (int64, error) {
	result := r.db.WithContext(ctx).Where("parent_type = ?", parentType).Delete(&model.EmbeddingChunk{})
	if result.Error != nil {
		log.Printf("[chunk] DeleteAllByType %s error: %v", parentType, result.Error)
		return 0, result.Error
	}
	log.Printf("[chunk] deleted %d chunks for type=%s", result.RowsAffected, parentType)
	return result.RowsAffected, nil
}
