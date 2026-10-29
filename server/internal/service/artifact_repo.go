package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ArtifactRepo — HTML 产物的 DB 操作。
//
// 列表读取**必须**走 FindRefsByIDs / ListByUser（都不 Select html）：列表一旦把正文带出来，
// 每次打开会话/灵阅就是 N 条 × 几十 KB 的持续性开销（见 docs/design/html-artifact §4.1）。
type ArtifactRepo struct {
	db *gorm.DB
}

func NewArtifactRepo(db *gorm.DB) *ArtifactRepo {
	return &ArtifactRepo{db: db}
}

func (r *ArtifactRepo) Create(ctx context.Context, a *model.Artifact) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// GetByID 取单条（含正文），按 user_id 鉴权。
func (r *ArtifactRepo) GetByID(ctx context.Context, userID, id string) (*model.Artifact, error) {
	var a model.Artifact
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// FindRefsByIDs 批量取标题/摘要，返回 id → ref。空入参返回空 map（不打 DB）。
func (r *ArtifactRepo) FindRefsByIDs(ctx context.Context, ids []string) (map[string]model.ArtifactRef, error) {
	out := make(map[string]model.ArtifactRef, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.Artifact
	if err := r.db.WithContext(ctx).
		Select("id", "title", "summary").
		Where("id IN ?", ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, a := range rows {
		out[a.ID] = model.ArtifactRef{ID: a.ID, Type: model.ArtifactTypeHTML, Title: a.Title, Summary: a.Summary}
	}
	return out, nil
}

// ListByUser — 灵阅（右栏「灵阅」）列表：按用户倒序，**不含正文**。
//
// 与 FindRefsByIDs 同一条硬规则：列表永远不 Select html（docs/design/html-artifact §4.1）。
// 软删由 GORM 默认的 deleted_at IS NULL 过滤，无痕会话销毁后不会出现在这里。
func (r *ArtifactRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Artifact, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.Artifact
	if err := r.db.WithContext(ctx).
		Select("id", "title", "summary", "conversation_id", "created_at").
		Where("user_id = ?", userID).
		Order("created_at DESC").Order("id DESC").
		Limit(limit).Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
