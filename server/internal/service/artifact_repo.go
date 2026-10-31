package service

import (
	"context"
	"strings"

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

// GetHTML — 裸 HTML 出口用：只取 id/title/html，避开最大 8MB 的 source_html。
func (r *ArtifactRepo) GetHTML(ctx context.Context, userID, id string) (*model.Artifact, error) {
	var a model.Artifact
	if err := r.db.WithContext(ctx).Select("id", "title", "html").
		Where("id = ? AND user_id = ?", id, userID).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// GetForRead — `html.get` 用：只取标题/摘要/标签/原文，避开最大 8MB 的 html 列。
// 原文为空（这次改动之前生成的页面）时，调用方再决定要不要读正文。
func (r *ArtifactRepo) GetForRead(ctx context.Context, userID, id string) (*model.Artifact, error) {
	var a model.Artifact
	if err := r.db.WithContext(ctx).
		Select("id", "title", "summary", "tags", "source_html").
		Where("id = ? AND user_id = ?", id, userID).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// GetMeta — 只取 id/title/summary/tags（改版沿用旧标题摘要标签用，不碰正文）。
func (r *ArtifactRepo) GetMeta(ctx context.Context, userID, id string) (*model.Artifact, error) {
	var a model.Artifact
	if err := r.db.WithContext(ctx).Select("id", "title", "summary", "tags").
		Where("id = ? AND user_id = ?", id, userID).First(&a).Error; err != nil {
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
	return r.ListByTags(ctx, userID, nil, "", limit, offset)
}

// ListByTags — 灵阅列表：多标签**交集** + 关键词（标题/摘要）+ 分页。
//
// limit == 0 表示只取总数、返回空列表（词云「查看 N 篇」用）；limit < 0 视为默认 30。
// 标签匹配复用 tagBoundaryClause（按逗号边界，防 "Go" 命中 "Golang"）。
// ponytail: tags 是逗号串、无索引，筛选走 user_id 定位 + 逐行 LIKE 全扫；灵阅量级（百级）无感，
// 上千页再规范化 artifact_tags 表，别提前建。
func (r *ArtifactRepo) ListByTags(ctx context.Context, userID string, tags []string, keyword string, limit, offset int) ([]model.Artifact, int64, error) {
	if limit < 0 {
		limit = 30
	}
	q := r.db.WithContext(ctx).Model(&model.Artifact{}).Where("user_id = ?", userID)
	for _, tag := range tags {
		clause, args := tagBoundaryClause(strings.ReplaceAll(tag, " ", ""))
		q = q.Where("("+clause+")", args...)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("(title LIKE ? OR summary LIKE ?)", like, like)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit == 0 { // GORM Limit(0) = 不加 LIMIT，必须显式早返回
		return []model.Artifact{}, total, nil
	}

	var rows []model.Artifact
	if err := q.Select("id", "title", "summary", "tags", "conversation_id", "created_at").
		Order("created_at DESC").Order("id DESC").
		Limit(limit).Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// TagGraph — 灵阅标签频次 + 共现边（词云用）。与文档共用 buildTagGraph 的统计口径。
func (r *ArtifactRepo) TagGraph(ctx context.Context, userID string) ([]TagCount, []TagEdge, error) {
	var raw []string
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("user_id = ? AND tags != ''", userID).
		Pluck("tags", &raw).Error; err != nil {
		return nil, nil, err
	}
	counts, edges := buildTagGraph(raw)
	return counts, edges, nil
}

// ListTags — 去重标签名（按频次降序），供 html.list_tags。
func (r *ArtifactRepo) ListTags(ctx context.Context, userID string) ([]string, error) {
	counts, _, err := r.TagGraph(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(counts))
	for _, c := range counts {
		out = append(out, c.Name)
	}
	return out, nil
}

// CountUntagged — 还没有标签的页数（必填规则上线前生成的老页），供工具提醒模型补标签。
// 老行由 ALTER TABLE 加列而来，值是 NULL 而不是空串，两种都要算。
func (r *ArtifactRepo) CountUntagged(ctx context.Context, userID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("user_id = ? AND (tags IS NULL OR tags = '')", userID).Count(&n).Error
	return n, err
}

// ExistingIDs — 批量取该用户下仍存在（未软删）的 id，删除结果里报告 missing 用。
func (r *ArtifactRepo) ExistingIDs(ctx context.Context, userID string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []string
	err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("user_id = ? AND id IN ?", userID, ids).
		Pluck("id", &out).Error
	return out, err
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
// 返回 false 表示不存在或不属于该用户。先直接 UPDATE，只有 0 行时才回查一次：
// MySQL 在新值与原值相同时 RowsAffected 也是 0，不能单凭它判「不存在」。
// 常见路径（内容真的变了）只走一条 SQL。
func (r *ArtifactRepo) Update(ctx context.Context, userID, id, title, summary, tags, html, sourceHTML string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{"title": title, "summary": summary, "tags": tags, "html": html, "source_html": sourceHTML})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}
	// 0 行：要么内容没变（行还在），要么真的不存在
	var exists int64
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Count(&exists).Error; err != nil {
		return false, err
	}
	return exists > 0, nil
}

// SetTags — 只改标签（html.patch 的 tags），不碰正文。返回 false 表示不存在或不属于该用户。
func (r *ArtifactRepo) SetTags(ctx context.Context, userID, id, tags string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("tags", tags)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}
	var exists int64
	if err := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Count(&exists).Error; err != nil {
		return false, err
	}
	return exists > 0, nil
}

// UpdateIfSourceUnchanged — 细粒度 patch 专用：只在原文未变时更新（乐观锁）。
//
// 返回 false 表示页面不存在、不属于该用户，或已被另一处改过。
// oldSource 是 patch 开始前读到的 source_html：它变了说明 nid/hash 已经错位，
// 这次写入必须失败（见 docs/design/html-artifact/fine-grained-edit.md §4.5）。
//
// 注意：MySQL 在「新旧值完全相同」时 RowsAffected 也是 0；调用方应先判断
// newSource == oldSource 直接返回成功，别把幂等重放误判成并发冲突。
//
// tags 与正文同一条 UPDATE 落库：分开写会出现「正文已改、标签失败」的半成功状态，
// 模型重试又被乐观锁判成「页面已变」，人话就变成「不是我改的吗」。
// tags 传 nil 表示「这次不动标签」（不传 tags 的 patch 走这里）；不要把读到的旧值写回去，
// 否则与「只改标签」的并发 patch 会互相回滚。
func (r *ArtifactRepo) UpdateIfSourceUnchanged(ctx context.Context, userID, id, oldSource, html, sourceHTML string, tags *string) (bool, error) {
	m := map[string]any{"html": html, "source_html": sourceHTML}
	// nil = 这次不动标签。别把调用方读到的旧值写回去，否则并发改的标签会被静默回滚。
	if tags != nil {
		m["tags"] = *tags
	}
	res := r.db.WithContext(ctx).Model(&model.Artifact{}).
		Where("id = ? AND user_id = ? AND source_html = ?", id, userID, oldSource).
		Updates(m)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
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
