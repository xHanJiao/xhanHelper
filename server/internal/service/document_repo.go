package service

import (
	"context"
	"sort"
	"strings"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// DocumentRepo — 文档数据访问
type DocumentRepo struct {
	db *gorm.DB
}

func NewDocumentRepo(db *gorm.DB) *DocumentRepo {
	return &DocumentRepo{db: db}
}

// Create 创建文档
func (r *DocumentRepo) Create(ctx context.Context, d *model.Document) error {
	return r.db.WithContext(ctx).Create(d).Error
}

// GetByID 按 ID + 用户 ID 查询单篇
func (r *DocumentRepo) GetByID(ctx context.Context, id, userID string) (*model.Document, error) {
	var doc model.Document
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// Update 部分更新文档
func (r *DocumentRepo) Update(ctx context.Context, id, userID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Document{}).Where("id = ? AND user_id = ?", id, userID).Updates(updates).Error
}

// GetByIDPublic 按 ID 查询文档（无用户限制，供公开分享使用）
func (r *DocumentRepo) GetByIDPublic(ctx context.Context, id string) (*model.Document, error) {
	var doc model.Document
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// Delete 删除文档，返回删除行数（0 表示文档不存在或不属于该用户）
func (r *DocumentRepo) Delete(ctx context.Context, id, userID string) (int64, error) {
	result := r.db.WithContext(ctx).Delete(&model.Document{}, "id = ? AND user_id = ?", id, userID)
	return result.RowsAffected, result.Error
}

// NormalizeTags 归一化标签串：按半角/全角逗号切分，去空格、去空项、保序去重，再用半角逗号 join。
// 写入路径（create/update）必须走这里，否则中文输入法下的全角逗号会被当成标签内容，
// 导致 list_tags/词云/tag 筛选整体错位。
func NormalizeTags(tags string) string {
	parts := splitTags(tags)
	if len(parts) == 0 {
		return ""
	}
	seen := make(map[string]bool, len(parts))
	uniq := parts[:0]
	for _, p := range parts {
		if seen[p] {
			continue
		}
		seen[p] = true
		uniq = append(uniq, p)
	}
	return strings.Join(uniq, ",")
}

// splitTags 按半角/全角逗号切分并去空格，跳过空项
func splitTags(tags string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == '，' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// tagMatchClause 按逗号边界匹配单个标签，避免子串误伤（"Go" 不该命中 "Golang"）。
// 用 REPLACE 去掉逗号旁空格，兼容历史数据里的 "a, b" 写法；tag 已在调用方去空格。
func tagMatchClause(q *gorm.DB, tag string) *gorm.DB {
	return q.Where(
		"REPLACE(tags, ' ', '') = ? OR REPLACE(tags, ' ', '') LIKE ? OR REPLACE(tags, ' ', '') LIKE ? OR REPLACE(tags, ' ', '') LIKE ?",
		tag, tag+",%", "%,"+tag, "%,"+tag+",%",
	)
}

// List 按条件分页查询。limit < 0 视为默认 50；limit == 0 表示只取总数、返回空列表。
// 排序必须带唯一 tie-break（id），否则同 updated_at 的文档在不同 limit 下顺序可能不同，翻页会重复/漏项。
func (r *DocumentRepo) List(ctx context.Context, userID, tag, keyword string, limit, offset int) ([]model.Document, int64, error) {
	if limit < 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if tag != "" {
		q = tagMatchClause(q, strings.ReplaceAll(tag, " ", ""))
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("title LIKE ? OR content LIKE ?", like, like)
	}

	var total int64
	if err := q.Model(&model.Document{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit == 0 { // GORM Limit(0) = 不加 LIMIT，必须显式早返回
		return []model.Document{}, total, nil
	}

	var list []model.Document
	err := q.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// TagCount 标签及其出现次数（用于词云）
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ListTagCounts 统计用户所有文档的标签出现次数，按次数降序、名称升序
func (r *DocumentRepo) ListTagCounts(ctx context.Context, userID string) ([]TagCount, error) {
	var raw []string
	err := r.db.WithContext(ctx).
		Model(&model.Document{}).
		Where("user_id = ? AND tags != ''", userID).
		Pluck("tags", &raw).Error
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for _, t := range raw {
		for _, part := range splitTags(t) {
			counts[part]++
		}
	}

	result := make([]TagCount, 0, len(counts))
	for name, count := range counts {
		result = append(result, TagCount{Name: name, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// ListTags 获取用户所有文档的去重标签列表（按出现次数降序）
func (r *DocumentRepo) ListTags(ctx context.Context, userID string) ([]string, error) {
	counts, err := r.ListTagCounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(counts))
	for _, tc := range counts {
		result = append(result, tc.Name)
	}
	return result, nil
}
