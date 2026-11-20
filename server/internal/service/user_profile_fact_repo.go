package service

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
	"xhan-helper/internal/profilefield"
)

// UserProfileFactRepo — 用户画像事实数据访问（field → fact 三级结构）。
type UserProfileFactRepo struct {
	db *gorm.DB
}

// NewUserProfileFactRepo 创建 UserProfileFactRepo。
func NewUserProfileFactRepo(db *gorm.DB) *UserProfileFactRepo {
	return &UserProfileFactRepo{db: db}
}

// FactInput — 一次提取产出的一条事实。
type FactInput struct {
	Value      string
	UserStated bool // 用户在本段对话里明确自陈（false = LLM 推断，先进 pending 观察）
}

// normFact — 事实文本归一化：去空白、统一小写。用于去重/命中判断，不落库。
func normFact(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// ReconcileFieldFacts 按「增量对账」语义写入某字段：LLM 输出的完整事实列表 = 该字段的目标状态。
//
//   - 仍在输出里的已有行：续命（last_seen_at = now，evidence_count + 1）
//   - pending 行再次出现：证据 +1，达到 2 次晋升 active
//   - 输出里消失的非用户行：置 outdated（invalidated_at = now），不物理删
//   - 新出现的行：用户自陈 → active，否则 pending（不进注入）
//   - source=user 行（用户手写）：LLM 永不覆盖、永不删除
//   - status=rejected 行（用户删掉的）：同文本永不复活
//
// 字段内的读/写全部按集合批处理，不逐条查库。
func (r *UserProfileFactRepo) ReconcileFieldFacts(ctx context.Context, userID, field string, inputs []FactInput, batchID string) error {
	// 归一化 + 去重（同文本只保留首个 UserStated=true 的判定）
	seen := make(map[string]struct{}, len(inputs))
	normed := make([]FactInput, 0, len(inputs))
	for _, in := range inputs {
		v := strings.TrimSpace(in.Value)
		if v == "" {
			continue
		}
		n := normFact(v)
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		normed = append(normed, FactInput{Value: v, UserStated: in.UserStated})
	}

	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.UserProfileFact
		if err := tx.Where("user_id = ? AND field = ? AND status IN ?", userID, field,
			[]string{model.FactStatusActive, model.FactStatusPending, model.FactStatusRejected}).
			Find(&rows).Error; err != nil {
			return err
		}

		byNorm := make(map[string]*model.UserProfileFact, len(rows))
		userNorms := make(map[string]struct{}, len(rows))
		rejectedNorms := make(map[string]struct{}, len(rows))
		for i := range rows {
			n := normFact(rows[i].Value)
			switch {
			case rows[i].Status == model.FactStatusRejected:
				rejectedNorms[n] = struct{}{}
				continue // 被删的行不参与匹配，否则会盖住用户后来重新加的同一文本
			case rows[i].Source == model.FactSourceUser:
				userNorms[n] = struct{}{}
			}
			byNorm[n] = &rows[i]
		}

		var toOutdate, toReaffirm, toPromote []string
		var toInsert []model.UserProfileFact
		matched := make(map[string]struct{}, len(normed))

		for _, in := range normed {
			n := normFact(in.Value)
			matched[n] = struct{}{}
			if _, isUser := userNorms[n]; isUser {
				continue // 用户行原样保留，不重复插入
			}
			row, ok := byNorm[n]
			if !ok {
				if _, bad := rejectedNorms[n]; bad {
					continue // 用户删过的，永不复活
				}
				fact := model.UserProfileFact{
					ID:            idgen.Next(),
					UserID:        userID,
					Field:         field,
					Value:         in.Value,
					Status:        model.FactStatusPending,
					BatchID:       batchID,
					ObservedAt:    &now,
					LastSeenAt:    &now,
					EvidenceCount: 1,
					Source:        model.FactSourceInferred,
				}
				if in.UserStated {
					fact.Status = model.FactStatusActive
					fact.Source = model.FactSourceUserStated
				}
				toInsert = append(toInsert, fact)
				continue
			}
			if row.Status == model.FactStatusPending && row.EvidenceCount+1 >= 2 {
				toPromote = append(toPromote, row.ID) // 观察够了 → 晋升 active
			} else {
				toReaffirm = append(toReaffirm, row.ID) // 仍在/再次出现 → 续命 +1 证据
			}
		}

		// 输出里消失的行 → 过时（用户行不动）
		for i := range rows {
			if rows[i].Source == model.FactSourceUser || rows[i].Status == model.FactStatusRejected {
				continue
			}
			if _, ok := matched[normFact(rows[i].Value)]; ok {
				continue
			}
			toOutdate = append(toOutdate, rows[i].ID)
		}

		if len(toOutdate) > 0 {
			if err := tx.Model(&model.UserProfileFact{}).Where("id IN ?", toOutdate).
				Updates(map[string]any{
					"status":         model.FactStatusOutdated,
					"batch_id":       batchID,
					"invalidated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		if len(toReaffirm) > 0 {
			// 仍在 / 再次出现：续命 + 证据 +1；status 不动（active 仍 active，pending 未达门槛仍 pending）
			if err := tx.Model(&model.UserProfileFact{}).Where("id IN ?", toReaffirm).
				Updates(map[string]any{
					"evidence_count": gorm.Expr("evidence_count + 1"),
					"last_seen_at":   now,
				}).Error; err != nil {
				return err
			}
		}
		if len(toPromote) > 0 {
			if err := tx.Model(&model.UserProfileFact{}).Where("id IN ?", toPromote).
				Updates(map[string]any{
					"evidence_count": gorm.Expr("evidence_count + 1"),
					"last_seen_at":   now,
					"status":         model.FactStatusActive,
				}).Error; err != nil {
				return err
			}
		}
		if len(toInsert) > 0 {
			if err := tx.Create(&toInsert).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceFieldFacts 全量替换某字段的事实集（种子/迁移用；生产写入走 ReconcileFieldFacts）。
// 语义等同"这些事实都是用户自陈"的对账：旧 active 全部过时、新值逐条写入 active，
// 用户手写行（source=user）与用户删掉的行（rejected）同样受保护。
func (r *UserProfileFactRepo) ReplaceFieldFacts(ctx context.Context, userID, field string, values []string, batchID string) error {
	inputs := make([]FactInput, 0, len(values))
	for _, v := range dedupeValues(values) {
		inputs = append(inputs, FactInput{Value: v, UserStated: true})
	}
	return r.ReconcileFieldFacts(ctx, userID, field, inputs, batchID)
}

// dedupeValues 防呆去重：trim 后过滤空值，相同内容只保留首次出现的顺序。
func dedupeValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// GetActiveByUser 返回用户全部 active 事实，按 created_at、id 升序（旧→新，顺序确定）。
func (r *UserProfileFactRepo) GetActiveByUser(ctx context.Context, userID string) ([]model.UserProfileFact, error) {
	var list []model.UserProfileFact
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, model.FactStatusActive).
		Order("created_at ASC, id ASC").
		Find(&list).Error
	return list, err
}

// ListAllByUser 返回用户全部事实（含 active / pending / outdated / rejected），备份与审计用。
func (r *UserProfileFactRepo) ListAllByUser(ctx context.Context, userID string) ([]model.UserProfileFact, error) {
	var list []model.UserProfileFact
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("field ASC, status ASC, created_at ASC, id ASC").
		Find(&list).Error
	return list, err
}

// GetActiveByField 返回用户某字段的全部 active 事实，按 created_at 升序。
func (r *UserProfileFactRepo) GetActiveByField(ctx context.Context, userID, field string) ([]model.UserProfileFact, error) {
	var list []model.UserProfileFact
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND field = ? AND status = ?", userID, field, model.FactStatusActive).
		Order("created_at ASC, id ASC").
		Find(&list).Error
	return list, err
}

// GetByID 按 id + user_id 取一条事实（用户编辑/删除前的归属校验）。
func (r *UserProfileFactRepo) GetByID(ctx context.Context, userID, id string) (*model.UserProfileFact, error) {
	var f model.UserProfileFact
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&f).Error
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// HasActive 检查用户是否有任何 active 事实（冷启动判断用）。
func (r *UserProfileFactRepo) HasActive(ctx context.Context, userID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.UserProfileFact{}).
		Where("user_id = ? AND status = ?", userID, model.FactStatusActive).
		Count(&count).Error
	return count > 0, err
}

// CreateUserFact 用户手动新增一条事实：直接 active、来源 user（LLM 不得覆盖）。
// 同字段里已有同文本的 pending / rejected 行时就地转正，避免同文本两行并存。
func (r *UserProfileFactRepo) CreateUserFact(ctx context.Context, userID, field, value string) (*model.UserProfileFact, error) {
	now := time.Now()
	value = strings.TrimSpace(value)
	var existing []model.UserProfileFact
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND field = ? AND status IN ?", userID, field,
			[]string{model.FactStatusActive, model.FactStatusPending, model.FactStatusRejected}).
		Find(&existing).Error; err != nil {
		return nil, err
	}
	want := normFact(value)
	for i := range existing {
		if normFact(existing[i].Value) != want {
			continue
		}
		if err := r.db.WithContext(ctx).Model(&model.UserProfileFact{}).
			Where("id = ?", existing[i].ID).
			Updates(map[string]any{
				"value":          value,
				"source":         model.FactSourceUser,
				"status":         model.FactStatusActive,
				"last_seen_at":   now,
				"invalidated_at": nil,
			}).Error; err != nil {
			return nil, err
		}
		existing[i].Value = value
		existing[i].Source = model.FactSourceUser
		existing[i].Status = model.FactStatusActive
		existing[i].LastSeenAt = &now
		return &existing[i], nil
	}
	f := &model.UserProfileFact{
		ID:            idgen.Next(),
		UserID:        userID,
		Field:         field,
		Value:         value,
		Status:        model.FactStatusActive,
		BatchID:       idgen.Next(),
		ObservedAt:    &now,
		LastSeenAt:    &now,
		EvidenceCount: 1,
		Source:        model.FactSourceUser,
	}
	if err := r.db.WithContext(ctx).Create(f).Error; err != nil {
		return nil, err
	}
	return f, nil
}

// UpdateUserFactValue 用户手动改一条事实：改值 + 标记来源 user（LLM 后续不得覆盖）。
// 返回 gorm.ErrRecordNotFound 表示该事实不属于该用户。
func (r *UserProfileFactRepo) UpdateUserFactValue(ctx context.Context, userID, id, value string, maxLen int) error {
	value = strings.TrimSpace(value)
	if maxLen > 0 {
		if runes := []rune(value); len(runes) > maxLen {
			value = string(runes[:maxLen])
		}
	}
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&model.UserProfileFact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{
			"value":        value,
			"source":       model.FactSourceUser,
			"status":       model.FactStatusActive,
			"last_seen_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// RejectFact 用户手动删除一条事实：置 rejected（不物理删），LLM 永不复活。
func (r *UserProfileFactRepo) RejectFact(ctx context.Context, userID, id string) error {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&model.UserProfileFact{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{
			"status":         model.FactStatusRejected,
			"source":         model.FactSourceUser,
			"invalidated_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// CountActiveByField 统计某字段 active 条数（新增前校验 MaxFacts）。
func (r *UserProfileFactRepo) CountActiveByField(ctx context.Context, userID, field string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UserProfileFact{}).
		Where("user_id = ? AND field = ? AND status = ?", userID, field, model.FactStatusActive).
		Count(&count).Error
	return count, err
}

// CleanupOutdated 清理 outdated 事实：按批次时间倒序保留最近 keepBatches 批，删除更旧的。
// 返回删除条数。
func (r *UserProfileFactRepo) CleanupOutdated(ctx context.Context, userID string, keepBatches int) (int64, error) {
	var batches []string
	err := r.db.WithContext(ctx).
		Model(&model.UserProfileFact{}).
		Select("batch_id").
		Where("user_id = ? AND status = ?", userID, model.FactStatusOutdated).
		Group("batch_id").
		Order("MAX(created_at) DESC").
		Pluck("batch_id", &batches).Error
	if err != nil {
		return 0, err
	}
	if len(batches) <= keepBatches {
		return 0, nil
	}
	stale := batches[keepBatches:]
	res := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ? AND batch_id IN ?", userID, model.FactStatusOutdated, stale).
		Delete(&model.UserProfileFact{})
	return res.RowsAffected, res.Error
}

// legacyFieldRenames — 画像 v2 字段改名表（旧名 → 新名）。只搬格子，不清内容。
// 「重要关系」在名单外：结构与容量都不动（未来换事件/实体机制）。
var legacyFieldRenames = map[string]string{
	"职业领域": "职业阶段",
	"财务概况": "财务轮廓",
	"饮食习惯": "饮食偏好",
}

// MigrateLegacyFields 把老数据搬到 v2 字段体系（幂等，启动时跑一次）：
//  1. 改名：旧字段名 → 新字段名
//  2. 非白名单字段的 active 行置 outdated（如「补充说明」），不物理删，可回滚
//
// 只搬格子、不改写内容（截断会让旧文本不可恢复）；配额收敛交给下一次 session_end。
// 返回 (改名行数, 置过时行数)。
func (r *UserProfileFactRepo) MigrateLegacyFields(ctx context.Context) (renamed, retired int64, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for oldField, newField := range legacyFieldRenames {
			res := tx.Model(&model.UserProfileFact{}).
				Where("field = ?", oldField).
				Update("field", newField)
			if res.Error != nil {
				return res.Error
			}
			renamed += res.RowsAffected
		}

		// 非白名单 active 行置 outdated（不删；「补充说明」等被删字段由这里退场）
		var fields []string
		if err := tx.Model(&model.UserProfileFact{}).
			Where("status = ?", model.FactStatusActive).
			Distinct().Pluck("field", &fields).Error; err != nil {
			return err
		}
		var unknown []string
		for _, f := range fields {
			if !profilefield.InProfileWhitelist(f) {
				unknown = append(unknown, f)
			}
		}
		if len(unknown) > 0 {
			res := tx.Model(&model.UserProfileFact{}).
				Where("field IN ? AND status = ?", unknown, model.FactStatusActive).
				Updates(map[string]any{"status": model.FactStatusOutdated})
			if res.Error != nil {
				return res.Error
			}
			retired += res.RowsAffected
		}

		// 有意不改写内容：截断/裁条会让旧文本不可恢复。超配额的旧条目留给下一次
		// session_end 的提取（filterFacts 按新配额重写）自然收敛，期间多几条注入无害。
		return nil
	})
	return renamed, retired, err
}

// BackfillFactTimestamps 回填 v2 时间列（幂等，启动时跑一次）：
// 老行没有 observed_at / last_seen_at，用 created_at 近似（历史时间被整字段重写刷新过）。
func (r *UserProfileFactRepo) BackfillFactTimestamps(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.UserProfileFact{}).
		Where("observed_at IS NULL").
		Updates(map[string]any{
			"observed_at":    gorm.Expr("created_at"),
			"last_seen_at":   gorm.Expr("created_at"),
			"evidence_count": gorm.Expr("CASE WHEN evidence_count < 1 THEN 1 ELSE evidence_count END"),
			"source":         gorm.Expr("CASE WHEN source = '' THEN ? ELSE source END", model.FactSourceInferred),
		})
	return res.RowsAffected, res.Error
}

// MarkBatchSource 把某一批写入的 active 事实打上来源标记（如 migrated）。
// 用于重建工具：让面板能一眼区分"迁移来的"与"对话提取的"。
func (r *UserProfileFactRepo) MarkBatchSource(ctx context.Context, userID, batchID, source string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.UserProfileFact{}).
		Where("user_id = ? AND batch_id = ? AND status = ?", userID, batchID, model.FactStatusActive).
		Update("source", source)
	return res.RowsAffected, res.Error
}
