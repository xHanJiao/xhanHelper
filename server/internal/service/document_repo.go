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

// Delete 删除文档
func (r *DocumentRepo) Delete(ctx context.Context, id, userID string) error {
	return r.db.WithContext(ctx).Delete(&model.Document{}, "id = ? AND user_id = ?", id, userID).Error
}

// tagMatchClause 按逗号边界匹配单个标签，避免子串误伤（"Go" 不该命中 "Golang"）。
// 用 REPLACE 去掉逗号旁空格，兼容历史数据里的 "a, b" 写法；tag 已在调用方去空格。
func tagMatchClause(q *gorm.DB, tag string) *gorm.DB {
	return q.Where(
		"REPLACE(tags, ' ', '') = ? OR REPLACE(tags, ' ', '') LIKE ? OR REPLACE(tags, ' ', '') LIKE ? OR REPLACE(tags, ' ', '') LIKE ?",
		tag, tag+",%", "%,"+tag, "%,"+tag+",%",
	)
}

// List 按条件分页查询，limit<=0 时默认 50
func (r *DocumentRepo) List(ctx context.Context, userID, tag, keyword string, limit, offset int) ([]model.Document, int64, error) {
	if limit <= 0 {
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

	var list []model.Document
	err := q.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&list).Error
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
		for _, part := range strings.Split(t, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				counts[part]++
			}
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
