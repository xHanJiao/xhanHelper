package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// ArtifactRepo — HTML 产物的 DB 操作。
//
// 列表读取**必须**走 FindRefsByIDs（Omit html）：消息列表一旦把正文带出来，
// 每次打开会话就是 N 条 × 几十 KB 的持续性开销（见 docs/design/html-artifact §4.1）。
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
