package service

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ImageAssetRepo — 图片资源数据访问
type ImageAssetRepo struct {
	db *gorm.DB
}

// NewImageAssetRepo 创建 ImageAssetRepo
func NewImageAssetRepo(db *gorm.DB) *ImageAssetRepo {
	return &ImageAssetRepo{db: db}
}

// Create 创建图片记录
func (r *ImageAssetRepo) Create(ctx context.Context, a *model.ImageAsset) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// GetByIDAndUser 按 ID + owner 查询（读取/删除都必须带 owner，防越权）
func (r *ImageAssetRepo) GetByIDAndUser(ctx context.Context, id, userID string) (*model.ImageAsset, error) {
	var a model.ImageAsset
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// GetByID 按 ID 查询（公开路由用，调用方必须自行校验 Visibility）
func (r *ImageAssetRepo) GetByID(ctx context.Context, id string) (*model.ImageAsset, error) {
	var a model.ImageAsset
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// GetByIDsAndUser 批量按 ID + owner 查询。
// 返回顺序不保证与入参一致，调用方需按需重建（图片顺序影响不大）。
func (r *ImageAssetRepo) GetByIDsAndUser(ctx context.Context, ids []string, userID string) ([]model.ImageAsset, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.ImageAsset
	err := r.db.WithContext(ctx).Where("id IN ? AND user_id = ?", ids, userID).Find(&list).Error
	return list, err
}

// FindBySHA 同用户内按内容指纹查重
func (r *ImageAssetRepo) FindBySHA(ctx context.Context, userID, sha string) (*model.ImageAsset, error) {
	var a model.ImageAsset
	if err := r.db.WithContext(ctx).Where("user_id = ? AND sha256 = ?", userID, sha).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// SetDeepSeekFileID 回填 Files API 的 file_id。
// 用字段名而非列名：列名由 GORM 命名策略决定（DeepSeekFileID → deep_seek_file_id），
// 手写列名一旦和策略不一致，这里会静默地每次都失败（图片反复重复上传）。
func (r *ImageAssetRepo) SetDeepSeekFileID(ctx context.Context, id, fileID string) error {
	return r.db.WithContext(ctx).Model(&model.ImageAsset{}).
		Where("id = ?", id).Update("DeepSeekFileID", fileID).Error
}

// SetDescription 回写图片描述 + 尝试时间。
func (r *ImageAssetRepo) SetDescription(ctx context.Context, id, desc string) error {
	return r.db.WithContext(ctx).Model(&model.ImageAsset{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{"description": desc, "description_at": time.Now()}).Error
}

// MarkDescriptionAttempted 只记录一次描述生成尝试（失败时用），避免失败图被反复重试。
func (r *ImageAssetRepo) MarkDescriptionAttempted(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.ImageAsset{}).
		Where("id = ? AND description_at IS NULL", id).
		Update("description_at", time.Now()).Error
}

// PromoteToDoc 把被文档引用的图片升级为可公开分享的文档配图：
// visibility=public + source=doc + referenced=true + 刷新 last_used_at。
// 只影响属于该用户的记录（不存在的 id 静默忽略，由调用方决定是否提示）。
func (r *ImageAssetRepo) PromoteToDoc(ctx context.Context, ids []string, userID string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.ImageAsset{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		Updates(map[string]interface{}{
			"visibility":   model.ImageVisibilityPublic,
			"source":       model.ImageSourceDoc,
			"referenced":   true,
			"last_used_at": time.Now(),
		}).Error
}

// MarkReferenced 批量标记为"已被消息引用"，刷新 last_used_at（清理任务的保留期基准）
func (r *ImageAssetRepo) MarkReferenced(ctx context.Context, ids []string, userID string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.ImageAsset{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		Updates(map[string]interface{}{"referenced": true, "last_used_at": time.Now()}).Error
}

// ListByUser 分页列出用户的图片（新的在前）
func (r *ImageAssetRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.ImageAsset, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&model.ImageAsset{}).
		Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ImageAsset
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// SearchByDescription 按描述关键词搜索（OR 语义，新的在前）。
// 用 INSTR 而不是 LIKE：关键词里的 % / _ 无需转义，MySQL 与 SQLite 都支持。
func (r *ImageAssetRepo) SearchByDescription(ctx context.Context, userID string, keywords []string, limit int) ([]model.ImageAsset, error) {
	if len(keywords) == 0 {
		return nil, nil
	}
	conds := make([]string, 0, len(keywords))
	args := make([]interface{}, 0, len(keywords))
	for _, kw := range keywords {
		conds = append(conds, "INSTR(LOWER(description), ?) > 0")
		args = append(args, strings.ToLower(kw))
	}
	var list []model.ImageAsset
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Where("("+strings.Join(conds, " OR ")+")", args...).
		Order("created_at DESC, id DESC").Limit(limit).Find(&list).Error
	return list, err
}

// ListDescribedAfter 键集分页列出有描述的图片（id > afterID，按 id 升序），供启动回填向量。
func (r *ImageAssetRepo) ListDescribedAfter(ctx context.Context, afterID string, limit int) ([]model.ImageAsset, error) {
	var list []model.ImageAsset
	err := r.db.WithContext(ctx).
		Where("description <> '' AND id > ?", afterID).
		Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// ExistingIDs 返回 ids 里真实存在的图片 id（孤儿向量对账用）。
func (r *ImageAssetRepo) ExistingIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []string
	err := r.db.WithContext(ctx).Model(&model.ImageAsset{}).
		Where("id IN ?", ids).Pluck("id", &out).Error
	return out, err
}

// Delete 按 ID + owner 删除记录（文件由调用方负责）
func (r *ImageAssetRepo) Delete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).
		Delete(&model.ImageAsset{}).Error
}

// DeleteBatch 批量删除记录（清理任务用），返回实际删除行数
func (r *ImageAssetRepo) DeleteBatch(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.ImageAsset{})
	return res.RowsAffected, res.Error
}

// ListBatchAfter 全局键集分页（id > afterID，按 id 升序）：清理任务对账用。
// 键集而非 OFFSET：对账边扫边删，OFFSET 会因删除而跳行。
func (r *ImageAssetRepo) ListBatchAfter(ctx context.Context, afterID string, limit int) ([]model.ImageAsset, error) {
	var list []model.ImageAsset
	err := r.db.WithContext(ctx).Where("id > ?", afterID).
		Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// ListStaleReferenced 列出被引用过、且最后使用时间早于 cutoff 的图（分批）。
// source=doc 的文档配图豁免（见 Cleanup 注释）。
func (r *ImageAssetRepo) ListStaleReferenced(ctx context.Context, cutoff time.Time, limit int) ([]model.ImageAsset, error) {
	var list []model.ImageAsset
	err := r.db.WithContext(ctx).
		Where("referenced = ? AND last_used_at < ? AND source <> ?", true, cutoff, model.ImageSourceDoc).
		Limit(limit).Find(&list).Error
	return list, err
}

// ListStaleOrphans 列出从未被引用、且上传时间早于 cutoff 的图（分批）。
// source=doc 的文档配图豁免。
func (r *ImageAssetRepo) ListStaleOrphans(ctx context.Context, cutoff time.Time, limit int) ([]model.ImageAsset, error) {
	var list []model.ImageAsset
	err := r.db.WithContext(ctx).
		Where("referenced = ? AND created_at < ? AND source <> ?", false, cutoff, model.ImageSourceDoc).
		Limit(limit).Find(&list).Error
	return list, err
}
