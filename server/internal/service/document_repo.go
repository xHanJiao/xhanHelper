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

// tagBoundaryClause 返回「按逗号边界匹配单个完整标签」的 SQL 片段与参数，避免子串误伤
// （"Go" 不该命中 "Golang"）。用 REPLACE 去掉逗号旁空格，兼容历史数据里的 "a, b" 写法；
// tag 已在调用方去空格。返回片段而非直接 Where，以便与多字段检索的 OR 组合复用。
func tagBoundaryClause(tag string) (string, []any) {
	return "REPLACE(tags, ' ', '') = ? OR REPLACE(tags, ' ', '') LIKE ? OR REPLACE(tags, ' ', '') LIKE ? OR REPLACE(tags, ' ', '') LIKE ?",
		[]any{tag, tag + ",%", "%," + tag, "%," + tag + ",%"}
}

// keywordClause 组合多字段 OR 检索条件。fields 取 title/tag/content，空集回退 title+content。
// title/content 走 LIKE 子串，tag 走精确边界匹配（与原 tag 筛选同语义）。
func keywordClause(keyword string, fields []string) (string, []any) {
	like := "%" + keyword + "%"
	want := map[string]bool{}
	for _, f := range fields {
		want[f] = true
	}
	if len(want) == 0 {
		want["title"], want["content"] = true, true
	}
	var conds []string
	var args []any
	if want["title"] {
		conds = append(conds, "title LIKE ?")
		args = append(args, like)
	}
	if want["content"] {
		conds = append(conds, "content LIKE ?")
		args = append(args, like)
	}
	if want["tag"] {
		clause, tagArgs := tagBoundaryClause(strings.ReplaceAll(keyword, " ", ""))
		conds = append(conds, "("+clause+")")
		args = append(args, tagArgs...)
	}
	return strings.Join(conds, " OR "), args
}

// List 单标签筛选（保留旧签名，供工具与测试使用），等价于 ListByAllTags 传一个标签。
func (r *DocumentRepo) List(ctx context.Context, userID, tag, keyword string, fields []string, limit, offset int) ([]model.Document, int64, error) {
	var tags []string
	if tag != "" {
		tags = []string{tag}
	}
	return r.ListByAllTags(ctx, userID, tags, keyword, fields, limit, offset)
}

// ListByAllTags 按多标签**交集**分页查询（文档必须同时命中全部所选标签，越选越少）。
// limit < 0 视为默认 50；limit == 0 表示只取总数、返回空列表。
// keyword 非空时按 fields 组合 OR 检索：title / tag（精确边界）/ content；
// fields 为空视为 [title, content]，保持旧行为。
// 排序必须带唯一 tie-break（id），否则同 updated_at 的文档在不同 limit 下顺序可能不同，翻页会重复/漏项。
//
// ponytail: tags 是逗号串列、无索引，筛选走 user_id 定位 + 逐行 REPLACE/LIKE 全扫（前导通配用不上 B-tree）。
// 实测 ~9µs/行：千篇 ~9ms、万篇 ~90ms。到万篇量级再规范化 document_tags + (user_id, tag) 复合索引，不要提前建。
func (r *DocumentRepo) ListByAllTags(ctx context.Context, userID string, tags []string, keyword string, fields []string, limit, offset int) ([]model.Document, int64, error) {
	if limit < 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if len(tags) > 0 {
		conds := make([]string, 0, len(tags))
		args := make([]any, 0, len(tags)*4)
		for _, tag := range tags {
			clause, tagArgs := tagBoundaryClause(strings.ReplaceAll(tag, " ", ""))
			conds = append(conds, "("+clause+")")
			args = append(args, tagArgs...)
		}
		q = q.Where(strings.Join(conds, " AND "), args...)
	}
	if keyword != "" {
		clause, args := keywordClause(keyword, fields)
		q = q.Where(clause, args...)
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

// TagEdge 两个标签的共现关系（同一篇文档里同时出现），供词云多选联动高亮建图。
type TagEdge struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Count int    `json:"count"`
}

// ListTagCounts 统计用户所有文档的标签出现次数，按次数降序、名称升序
func (r *DocumentRepo) ListTagCounts(ctx context.Context, userID string) ([]TagCount, error) {
	counts, _, err := r.tagGraph(ctx, userID)
	return counts, err
}

// TagGraph 返回标签频次 + 共现边（一次全扫同时算出，不额外查库）。
func (r *DocumentRepo) TagGraph(ctx context.Context, userID string) ([]TagCount, []TagEdge, error) {
	return r.tagGraph(ctx, userID)
}

// tagGraph 一次 Pluck 出该用户所有 tags 串，在内存里同时统计频次与两两共现。
// 每篇文档先按逗号切分并去重，再对去重后的标签做两两计数（k 通常 2~8，k² 可忽略）。
func (r *DocumentRepo) tagGraph(ctx context.Context, userID string) ([]TagCount, []TagEdge, error) {
	var raw []string
	err := r.db.WithContext(ctx).
		Model(&model.Document{}).
		Where("user_id = ? AND tags != ''", userID).
		Pluck("tags", &raw).Error
	if err != nil {
		return nil, nil, err
	}

	type pair struct{ a, b string }
	counts := make(map[string]int)
	edges := make(map[pair]int)
	for _, t := range raw {
		parts := splitTags(t)
		seen := make(map[string]bool, len(parts))
		uniq := make([]string, 0, len(parts))
		for _, part := range parts {
			counts[part]++ // 保持旧语义：按出现次数计（历史数据可能同篇重复）
			if seen[part] {
				continue
			}
			seen[part] = true
			uniq = append(uniq, part)
		}
		for i := 0; i < len(uniq); i++ {
			for j := i + 1; j < len(uniq); j++ {
				a, b := uniq[i], uniq[j]
				if a > b {
					a, b = b, a
				}
				edges[pair{a, b}]++
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

	edgeList := make([]TagEdge, 0, len(edges))
	for p, count := range edges {
		edgeList = append(edgeList, TagEdge{A: p.a, B: p.b, Count: count})
	}
	sort.Slice(edgeList, func(i, j int) bool {
		if edgeList[i].A != edgeList[j].A {
			return edgeList[i].A < edgeList[j].A
		}
		return edgeList[i].B < edgeList[j].B
	})
	return result, edgeList, nil
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
