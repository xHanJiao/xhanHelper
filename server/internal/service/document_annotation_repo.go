package service

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"xhan-helper/internal/model"
)

// DocumentAnnotationRepo — 文档划线批注数据访问
type DocumentAnnotationRepo struct {
	db *gorm.DB
}

func NewDocumentAnnotationRepo(db *gorm.DB) *DocumentAnnotationRepo {
	return &DocumentAnnotationRepo{db: db}
}

// Create 新建批注
func (r *DocumentAnnotationRepo) Create(ctx context.Context, a *model.DocumentAnnotation) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// GetByID 按 ID + 用户 ID 查单条
func (r *DocumentAnnotationRepo) GetByID(ctx context.Context, id, userID string) (*model.DocumentAnnotation, error) {
	var a model.DocumentAnnotation
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// List 按文档分页查询。limit < 0 视为默认 200；limit == 0 表示只取总数、返回空列表。
// 排序带 id 作 tie-break，避免同 start_offset 的批注翻页时重复/漏项。
func (r *DocumentAnnotationRepo) List(ctx context.Context, docID, userID string, limit, offset int) ([]model.DocumentAnnotation, int64, error) {
	if limit < 0 {
		limit = 200
	}
	q := r.db.WithContext(ctx).Model(&model.DocumentAnnotation{}).
		Where("document_id = ? AND user_id = ?", docID, userID)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit == 0 { // GORM Limit(0) = 不加 LIMIT，必须显式早返回
		return []model.DocumentAnnotation{}, total, nil
	}

	var list []model.DocumentAnnotation
	err := q.Order("start_offset ASC, id ASC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// CountByDocument 该文档已有多少条批注（配额校验用）
func (r *DocumentAnnotationRepo) CountByDocument(ctx context.Context, docID, userID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.DocumentAnnotation{}).
		Where("document_id = ? AND user_id = ?", docID, userID).Count(&n).Error
	return n, err
}

// UpdateNote 更新批注正文（空串 = 退化成纯划线，不删实体）
func (r *DocumentAnnotationRepo) UpdateNote(ctx context.Context, id, userID, note string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.DocumentAnnotation{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("note", note)
	return res.RowsAffected, res.Error
}

// UpdateAnchor 手动重挂锚点（抽屉「重新定位」）。
// end 由调用方从 Anchorer.Relocate 取得：跨 Markdown 标记时 quote 在源文里的
// 实际跨度比 quote 本身长，不能靠 len(quote) 推。
func (r *DocumentAnnotationRepo) UpdateAnchor(ctx context.Context, id, userID string, a Anchor, end int) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.DocumentAnnotation{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"quote":           a.Quote,
			"prefix":          a.Prefix,
			"suffix":          a.Suffix,
			"start_offset":    a.Start,
			"end_offset":      end,
			"orphaned":        false,
			"quote_rewritten": false,
		})
	return res.RowsAffected, res.Error
}

// Delete 删除单条
func (r *DocumentAnnotationRepo) Delete(ctx context.Context, id, userID string) (int64, error) {
	res := r.db.WithContext(ctx).Delete(&model.DocumentAnnotation{}, "id = ? AND user_id = ?", id, userID)
	return res.RowsAffected, res.Error
}

// RelocateAll 正文变化后重算该文档全部批注的锚点。
// 返回 (失活状态发生变化的条数, 其中新失活的条数)。
// 已失活的也重算，正文改回来时能自动恢复。
//
// 定位成功时顺手刷新 quote / prefix / suffix 快照：让锚点始终描述"当前正文"，
// 下一次改写时才是有效的参照，不会累积漂移。夹逼改写（Fuzzy）同样刷新，
// 并把 quote_rewritten 置位，让"动了用户数据"这件事在界面上可见。
func (r *DocumentAnnotationRepo) RelocateAll(ctx context.Context, docID, userID, content string) (int, int, error) {
	var list []model.DocumentAnnotation
	if err := r.db.WithContext(ctx).
		Where("document_id = ? AND user_id = ?", docID, userID).Find(&list).Error; err != nil {
		return 0, 0, err
	}
	if len(list) == 0 {
		return 0, 0, nil
	}

	ax := NewAnchorer(content)
	changed := make([]model.DocumentAnnotation, 0, 4)
	affected, orphaned := 0, 0

	for i := range list {
		a := &list[i]
		sp, ok := ax.Relocate(Anchor{
			Quote: a.Quote, Prefix: a.Prefix, Suffix: a.Suffix,
			Start: a.StartOffset, End: a.EndOffset,
		})

		if !ok {
			if a.Orphaned {
				continue
			}
			affected++
			orphaned++
			a.Orphaned = true
			changed = append(changed, *a)
			continue
		}

		quote, pfx, sfx := a.Quote, a.Prefix, a.Suffix
		if sp.Fuzzy {
			quote = sp.Quote
		}
		pfx, sfx = ax.Context(sp)

		if !a.Orphaned && a.StartOffset == sp.Start && a.EndOffset == sp.End &&
			a.Quote == quote && a.Prefix == pfx && a.Suffix == sfx &&
			a.QuoteRewritten == sp.Fuzzy {
			continue
		}
		if a.Orphaned {
			affected++
		}
		a.Orphaned = false
		a.StartOffset, a.EndOffset = sp.Start, sp.End
		a.Quote, a.Prefix, a.Suffix = quote, pfx, sfx
		a.QuoteRewritten = sp.Fuzzy
		changed = append(changed, *a)
	}

	if len(changed) == 0 {
		return 0, 0, nil
	}
	// 逐行 UPDATE 会撞 N+1 约束，用一次 upsert 覆盖本批（只可能是变化的少数几行）。
	// ponytail: UpdateAll 会重写全部列，批注列少、代价可忽略；真变大表再改 CASE WHEN。
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&changed).Error; err != nil {
		return 0, 0, err
	}
	return affected, orphaned, nil
}
