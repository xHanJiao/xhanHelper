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

// Delete 软删一条（按 user_id 鉴权）。返回受影响行数，0 表示不存在或不属于该用户。
//
// 主键放进 Model（活动埋点规则）：GORM 回调要从模型读结构化主键。
// 分享 token 在同一行，删掉即失效——公开路由也走默认的 deleted_at 过滤。
func (r *ArtifactRepo) Delete(ctx context.Context, userID, id string) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&model.Artifact{ID: id})
	return res.RowsAffected, res.Error
}

// Update — 原地覆盖一页（「改这一版」，见 design §4.2）。
//
// 返回 false 表示不存在或不属于该用户。**不能拿 RowsAffected 判存在**：
// MySQL 在新值与原值相同时返回 0，会把一次成功的空改误判成 404。
// 所以先 Count 一次再更新（多一次索引查询，换掉一类隐蔽误判）。
func (r *ArtifactRepo) Update(ctx context.Context, userID, id, title, summary, html string) (bool, error) {
	var exists int64
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Count(&exists).Error; err != nil {
		return false, err
	}
	if exists == 0 {
		return false, nil
	}
	err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{"title": title, "summary": summary, "html": html}).Error
	if err != nil {
		return false, err
	}
	return true, nil
}

// DeleteMany — 批量软删（灵阅多选删除）。只删属于该用户的，返回实际删除行数。
// 空入参直接返回 0：`IN ()` 在部分方言里是语法错误，别让空选择变成 500。
func (r *ArtifactRepo) DeleteMany(ctx context.Context, userID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Where("user_id = ? AND id IN ?", userID, ids).
		Delete(&model.Artifact{})
	return res.RowsAffected, res.Error
}

// SetShareToken 设置分享 token（幂等：已有则原地覆盖）。返回受影响行数，0 表示不存在。
func (r *ArtifactRepo) SetShareToken(ctx context.Context, userID, id, token string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("share_token", token)
	return res.RowsAffected, res.Error
}

// ClearShareToken 撤销分享。已撤销时 rows 为 0（调用方据此判 void 情况）。
func (r *ArtifactRepo) ClearShareToken(ctx context.Context, userID, id string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("share_token", "")
	return res.RowsAffected, res.Error
}

// GetByShareToken 按 token 取一条（含正文，公开路由要用）。
// 软删的行取不到（GORM 默认过滤）→ 删除后旧链接自动 404。
func (r *ArtifactRepo) GetByShareToken(ctx context.Context, token string) (*model.Artifact, error) {
	var a model.Artifact
	if err := r.db.WithContext(ctx).Where("share_token = ?", token).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// GetShareToken 只取 token（不碰正文）。重复点「分享」时复用它，别把已发出去的旧链接作废。
func (r *ArtifactRepo) GetShareToken(ctx context.Context, userID, id string) (string, error) {
	var a model.Artifact
	if err := r.db.WithContext(ctx).Select("share_token").
		Where("id = ? AND user_id = ?", id, userID).First(&a).Error; err != nil {
		return "", err
	}
	return a.ShareToken, nil
}
