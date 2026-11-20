package service

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
)

func setupFactRepo(t *testing.T) *UserProfileFactRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfileFact{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return NewUserProfileFactRepo(db)
}

func TestFactRepo_ReplaceFieldFacts(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	if err := repo.ReplaceFieldFacts(ctx, "u1", "重要关系", []string{"CJ: 伴侣", "又又: 好友"}, "b1"); err != nil {
		t.Fatalf("replace round 1: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "重要关系")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active facts, got %d", len(active))
	}

	// 第二轮替换：又又保留、CJ 淘汰、新增惠茜
	if err := repo.ReplaceFieldFacts(ctx, "u1", "重要关系", []string{"又又: 好友", "惠茜: 表妹"}, "b2"); err != nil {
		t.Fatalf("replace round 2: %v", err)
	}
	active, err = repo.GetActiveByField(ctx, "u1", "重要关系")
	if err != nil {
		t.Fatalf("get active round 2: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active facts after round 2, got %d", len(active))
	}
	got := map[string]bool{}
	for _, f := range active {
		got[f.Value] = true
		if f.Status != model.FactStatusActive {
			t.Errorf("fact %q should be active, got %s", f.Value, f.Status)
		}
		// 续命的事实保留原批次（不对账不重写），新增事实才带新批次
		if f.Value == "惠茜: 表妹" && f.BatchID != "b2" {
			t.Errorf("new fact %q batch = %s, expected b2", f.Value, f.BatchID)
		}
	}
	if !got["又又: 好友"] || !got["惠茜: 表妹"] {
		t.Errorf("expected 又又 and 惠茜 active, got %v", got)
	}
	if got["CJ: 伴侣"] {
		t.Error("CJ should be outdated")
	}

	// outdated 审计可查
	all, err := repo.GetActiveByField(ctx, "u1", "重要关系")
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 total facts, got %d", len(all))
	}
}

func TestFactRepo_ReplaceFieldFacts_IsolatedByUserAndField(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	if err := repo.ReplaceFieldFacts(ctx, "u1", "职业领域", []string{"Go后端"}, "b1"); err != nil {
		t.Fatalf("seed u1: %v", err)
	}
	if err := repo.ReplaceFieldFacts(ctx, "u2", "职业领域", []string{"医生"}, "b1"); err != nil {
		t.Fatalf("seed u2: %v", err)
	}
	if err := repo.ReplaceFieldFacts(ctx, "u1", "饮食习惯", []string{"不吃辣"}, "b1"); err != nil {
		t.Fatalf("seed u1 field2: %v", err)
	}

	// u2 的替换不影响 u1
	if err := repo.ReplaceFieldFacts(ctx, "u2", "职业领域", []string{"牙医"}, "b2"); err != nil {
		t.Fatalf("replace u2: %v", err)
	}
	u1Career, err := repo.GetActiveByField(ctx, "u1", "职业领域")
	if err != nil {
		t.Fatalf("get u1 career: %v", err)
	}
	if len(u1Career) != 1 || u1Career[0].Value != "Go后端" {
		t.Errorf("u1 career should be unchanged, got %+v", u1Career)
	}
	u1Diet, err := repo.GetActiveByField(ctx, "u1", "饮食习惯")
	if err != nil {
		t.Fatalf("get u1 diet: %v", err)
	}
	if len(u1Diet) != 1 || u1Diet[0].Value != "不吃辣" {
		t.Errorf("u1 diet should be unchanged, got %+v", u1Diet)
	}
}

func TestFactRepo_HasActive(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	ok, err := repo.HasActive(ctx, "u1")
	if err != nil {
		t.Fatalf("has active: %v", err)
	}
	if ok {
		t.Error("empty user should have no active facts")
	}

	if err := repo.ReplaceFieldFacts(ctx, "u1", "称呼", []string{"哥"}, "b1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ok, err = repo.HasActive(ctx, "u1")
	if err != nil {
		t.Fatalf("has active 2: %v", err)
	}
	if !ok {
		t.Error("seeded user should have active facts")
	}

	// 全量替换后（仍有 active）
	if err := repo.ReplaceFieldFacts(ctx, "u1", "称呼", []string{"老哥"}, "b2"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	ok, err = repo.HasActive(ctx, "u1")
	if err != nil {
		t.Fatalf("has active 3: %v", err)
	}
	if !ok {
		t.Error("user should still have active facts after replace")
	}
}

func TestFactRepo_ReplaceFieldFacts_DedupeAndEmpty(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	err := repo.ReplaceFieldFacts(ctx, "u1", "补充说明",
		[]string{"睡眠差", "睡眠差", "", "  房价观察  ", "房价观察"}, "b1")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "补充说明")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 unique facts, got %d: %+v", len(active), active)
	}
	if active[0].Value != "睡眠差" {
		t.Errorf("first fact should be 睡眠差, got %q", active[0].Value)
	}
	if active[1].Value != "房价观察" {
		t.Errorf("second fact should be trimmed 房价观察, got %q", active[1].Value)
	}
}

func TestFactRepo_CleanupOutdated(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	// 4 轮替换 → 4 批 outdated（b0~b2 是旧批，b3 是当前 active 的批）
	for i, batch := range []string{"b0", "b1", "b2", "b3"} {
		_ = i
		if err := repo.ReplaceFieldFacts(ctx, "u1", "职业领域", []string{"fact-" + batch}, batch); err != nil {
			t.Fatalf("replace %s: %v", batch, err)
		}
	}
	// 此时 outdated 批次 = b0, b1, b2（3 批），active = b3
	// keepBatches=2 → 删最旧的 b0
	n, err := repo.CleanupOutdated(ctx, "u1", 2)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 deleted fact, got %d", n)
	}

	// b0 的 fact 没了，b1/b2 还在
	var outdated []model.UserProfileFact
	if err := repo.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", "u1", model.FactStatusOutdated).
		Find(&outdated).Error; err != nil {
		t.Fatalf("query outdated: %v", err)
	}
	if len(outdated) != 2 {
		t.Fatalf("expected 2 outdated facts left, got %d", len(outdated))
	}
	for _, f := range outdated {
		if f.BatchID == "b0" {
			t.Errorf("b0 batch should be cleaned, got %+v", f)
		}
	}

	// active 不受影响
	active, err := repo.GetActiveByField(ctx, "u1", "职业领域")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 1 || active[0].Value != "fact-b3" {
		t.Errorf("active should be fact-b3, got %+v", active)
	}
}

func init() {
	idgen.Init(1)
}

func TestFactRepo_Reconcile_EvidenceGate(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	// 第一次只是 LLM 推断 → pending，不进 active（也就不进注入）
	if err := repo.ReconcileFieldFacts(ctx, "u1", "饮食偏好",
		[]FactInput{{Value: "不吃辣", UserStated: false}}, "b1"); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "饮食偏好")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("inferred fact should stay pending (not injected), got %+v", active)
	}

	// 再次被提及 → 证据 2 → 晋升 active
	if err := repo.ReconcileFieldFacts(ctx, "u1", "饮食偏好",
		[]FactInput{{Value: "不吃辣", UserStated: false}}, "b2"); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	active, err = repo.GetActiveByField(ctx, "u1", "饮食偏好")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 1 || active[0].Value != "不吃辣" {
		t.Fatalf("expected promoted fact 不吃辣, got %+v", active)
	}
	if active[0].EvidenceCount != 2 {
		t.Errorf("evidence_count = %d, want 2", active[0].EvidenceCount)
	}
	if active[0].LastSeenAt == nil || active[0].ObservedAt == nil {
		t.Error("timestamps should be filled")
	}
}

func TestFactRepo_Reconcile_ProtectsUserAndRejected(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	userFact, err := repo.CreateUserFact(ctx, "u1", "协作偏好", "结论先行")
	if err != nil {
		t.Fatalf("create user fact: %v", err)
	}
	// LLM 输出里没有用户手写那条 → 用户行仍保留
	if err := repo.ReconcileFieldFacts(ctx, "u1", "协作偏好",
		[]FactInput{{Value: "别写客套话", UserStated: true}}, "b1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "协作偏好")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	got := map[string]bool{}
	var llmFactID string
	for _, f := range active {
		got[f.Value] = true
		if f.Value == "别写客套话" {
			llmFactID = f.ID
		}
	}
	if !got["结论先行"] {
		t.Error("user-written fact must survive LLM reconcile")
	}
	if !got["别写客套话"] {
		t.Error("new llm fact should be active")
	}

	// 用户删掉 LLM 那条 → rejected，之后 LLM 再输出同文本也不复活
	if err := repo.RejectFact(ctx, "u1", llmFactID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := repo.ReconcileFieldFacts(ctx, "u1", "协作偏好",
		[]FactInput{{Value: "别写客套话", UserStated: true}}, "b2"); err != nil {
		t.Fatalf("reconcile after reject: %v", err)
	}
	active, err = repo.GetActiveByField(ctx, "u1", "协作偏好")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	for _, f := range active {
		if f.Value == "别写客套话" {
			t.Error("rejected fact must never revive")
		}
	}
	if len(active) != 1 || active[0].ID != userFact.ID {
		t.Errorf("only the user-written fact should remain active, got %+v", active)
	}
}

func TestFactRepo_MigrateLegacyFields(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()
	if err := repo.db.Create(&[]model.UserProfileFact{
		{ID: "f1", UserID: "u1", Field: "职业领域", Value: "5年Go后端", Status: model.FactStatusActive, BatchID: "b1"},
		{ID: "f2", UserID: "u1", Field: "补充说明", Value: "杂项", Status: model.FactStatusActive, BatchID: "b1"},
		{ID: "f3", UserID: "u1", Field: "重要关系", Value: "CJ: 伴侣", Status: model.FactStatusActive, BatchID: "b1"},
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	renamed, retired, err := repo.MigrateLegacyFields(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if renamed != 1 || retired != 1 {
		t.Errorf("renamed=%d retired=%d, want 1/1", renamed, retired)
	}
	active, err := repo.GetActiveByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	got := map[string]string{}
	for _, f := range active {
		got[f.Field] = f.Value
	}
	if got["职业阶段"] != "5年Go后端" {
		t.Errorf("职业领域 should be renamed to 职业阶段, got %v", got)
	}
	if _, ok := got["补充说明"]; ok {
		t.Error("补充说明 should be retired (outdated), not active")
	}
	if got["重要关系"] != "CJ: 伴侣" {
		t.Error("重要关系 must stay untouched")
	}
}

// 用户删掉一条后又手动加回同一文本：rejected 行不能盖住新加的 active 行（否则下次提取会把它判成过期）。
func TestFactRepo_Reconcile_ReAddedAfterRejectStaysActive(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()

	if err := repo.ReconcileFieldFacts(ctx, "u1", "硬约束",
		[]FactInput{{Value: "不吃辣", UserStated: true}}, "b1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "硬约束")
	if err != nil || len(active) != 1 {
		t.Fatalf("seed active: %v %+v", err, active)
	}
	if err := repo.RejectFact(ctx, "u1", active[0].ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	readded, err := repo.CreateUserFact(ctx, "u1", "硬约束", "不吃辣")
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}

	// LLM 下一轮又输出同一文本
	if err := repo.ReconcileFieldFacts(ctx, "u1", "硬约束",
		[]FactInput{{Value: "不吃辣", UserStated: true}}, "b2"); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	active, err = repo.GetActiveByField(ctx, "u1", "硬约束")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 1 || active[0].ID != readded.ID {
		t.Fatalf("re-added user fact must stay the only active row, got %+v", active)
	}
}

// 迁移只搬格子、不改写内容：超配额的老条目原样保留（截断会让旧文本不可恢复），
// 留给下一次 session_end 的提取按新配额自然收敛。
func TestFactRepo_MigrateLegacyFields_KeepsValues(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()
	long := strings.Repeat("字", 60)
	var rows []model.UserProfileFact
	for i := 0; i < 6; i++ {
		rows = append(rows, model.UserProfileFact{
			ID: "f" + string(rune('a'+i)), UserID: "u1", Field: "职业领域",
			Value: long, Status: model.FactStatusActive, BatchID: "b1",
		})
	}
	if err := repo.db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := repo.MigrateLegacyFields(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "职业阶段")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 6 {
		t.Fatalf("migration must not drop facts, got %d", len(active))
	}
	for _, f := range active {
		if n := len([]rune(f.Value)); n != 60 {
			t.Errorf("migration must not rewrite values, got %d runes", n)
		}
	}
}

// 用户新增的文本已有一条 pending 时：就地转正，不再插第二行。
func TestFactRepo_CreateUserFact_PromotesPendingSameText(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()
	if err := repo.ReconcileFieldFacts(ctx, "u1", "饮食偏好",
		[]FactInput{{Value: "不吃辣", UserStated: false}}, "b1"); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	f, err := repo.CreateUserFact(ctx, "u1", "饮食偏好", " 不吃辣 ")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	active, err := repo.GetActiveByField(ctx, "u1", "饮食偏好")
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("should reuse the pending row, got %d active", len(active))
	}
	if active[0].ID != f.ID || active[0].Source != model.FactSourceUser {
		t.Errorf("reused row should be the same id and source=user, got %+v", active[0])
	}
}

// active 行被下一轮提取原样复述：续命 + 证据 +1。
func TestFactRepo_Reconcile_ActiveReaffirmBumpsEvidence(t *testing.T) {
	repo := setupFactRepo(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := repo.ReconcileFieldFacts(ctx, "u1", "硬约束",
			[]FactInput{{Value: "对海鲜过敏", UserStated: true}}, "b"+string(rune('1'+i))); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
	active, err := repo.GetActiveByField(ctx, "u1", "硬约束")
	if err != nil || len(active) != 1 {
		t.Fatalf("get active: %v %+v", err, active)
	}
	if active[0].EvidenceCount != 2 {
		t.Errorf("evidence_count = %d, want 2", active[0].EvidenceCount)
	}
}
