package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// UserMemoryRepo — 用户记忆数据访问
type UserMemoryRepo struct {
	db *gorm.DB
}

// NewUserMemoryRepo 创建 UserMemoryRepo
func NewUserMemoryRepo(db *gorm.DB) *UserMemoryRepo {
	return &UserMemoryRepo{db: db}
}

// Create 创建新记忆
func (r *UserMemoryRepo) Create(ctx context.Context, m *model.UserMemory) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindByKey 按 key 查找已有记忆
func (r *UserMemoryRepo) FindByKey(ctx context.Context, userID, key string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND memory_key = ?", userID, key).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// ListByUser 分页获取用户记忆，支持关键词搜索，limit<=0 时默认 50
func (r *UserMemoryRepo) ListByUser(ctx context.Context, userID, keyword, source string, limit, offset int) ([]model.UserMemory, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("memory_value LIKE ? OR memory_key LIKE ?", like, like)
	}
	if source != "" {
		q = q.Where("source = ?", source)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.UserMemory
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// FindByID 按 ID 查单条记忆
func (r *UserMemoryRepo) FindByID(ctx context.Context, userID, id string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// Delete 删除记忆
func (r *UserMemoryRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.UserMemory{}, "id = ?", id).Error
}

// BatchDelete 批量删除记忆（按 ID + 用户过滤）
func (r *UserMemoryRepo) BatchDelete(ctx context.Context, ids []string, userID string) error {
	return r.db.WithContext(ctx).Where("id IN ? AND user_id = ?", ids, userID).Delete(&model.UserMemory{}).Error
}

// UpdateEmbedding 更新记忆的向量字段
func (r *UserMemoryRepo) UpdateEmbedding(ctx context.Context, id string, embedding *string) error {
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).Where("id = ?", id).
		Update("embedding", embedding).Error
}

// GetByIDs 按 ID 列表查询
func (r *UserMemoryRepo) GetByIDs(ctx context.Context, ids []string) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// GetByIDsAndUser 按 ID 列表 + 用户 ID 查询
func (r *UserMemoryRepo) GetByIDsAndUser(ctx context.Context, ids []string, userID string) ([]model.UserMemory, error) {
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Where("id IN ? AND user_id = ?", ids, userID).Find(&list).Error
	return list, err
}

// FindByPromptHash 按 prompt 哈希查找已有记忆（输入去重）
func (r *UserMemoryRepo) FindByPromptHash(ctx context.Context, userID, hash string) (*model.UserMemory, error) {
	var m model.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND prompt_hash = ?", userID, hash).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &m, err
}

// ListAllPaginated 分页获取所有记忆（用于 warmup 加载向量库），limit<=0 时默认 100
func (r *UserMemoryRepo) ListAllPaginated(ctx context.Context, limit, offset int) ([]model.UserMemory, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []model.UserMemory
	err := r.db.WithContext(ctx).Limit(limit).Offset(offset).Find(&list).Error
	return list, err
}

// UpdateConfirm 更新确认计数
func (r *UserMemoryRepo) UpdateConfirm(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.UserMemory{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"confirm_count":     gorm.Expr("confirm_count + 1"),
			"last_confirmed_at": gorm.Expr("CURRENT_TIMESTAMP"),
		}).Error
}
