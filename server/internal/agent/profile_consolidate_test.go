package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// consolidateTestEnv — 重写测试用的完整依赖（内存 sqlite）。
type consolidateTestEnv struct {
	c         *ProfileConsolidator
	db        *gorm.DB
	facts     *service.UserProfileFactRepo
	state     *service.ProfileRewriteStateRepo
	backupDir string
}

func setupConsolidator(t *testing.T, llmClient llm.LLMClient) *consolidateTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfileFact{}, &model.Event{}, &model.Conversation{},
		&model.UserMemory{}, &model.ProfileRewriteState{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	backupDir := t.TempDir()
	factRepo := service.NewUserProfileFactRepo(db)
	stateRepo := service.NewProfileRewriteStateRepo(db)
	c := NewProfileConsolidator(&ProfileConsolidatorConfig{
		Client:    llmClient,
		FactRepo:  factRepo,
		EventRepo: service.NewEventRepo(db),
		MemRepo:   service.NewUserMemoryRepo(db),
		ConvRepo:  service.NewConversationRepo(db),
		UserRepo:  service.NewUserRepo(db, 60, 8),
		StateRepo: stateRepo,
		BackupDir: backupDir,
		DBName:    "test",
	})
	return &consolidateTestEnv{c: c, db: db, facts: factRepo, state: stateRepo, backupDir: backupDir}
}

// activeValues 取某字段生效事实的文本。
func (e *consolidateTestEnv) activeValues(t *testing.T, userID, field string) []string {
	t.Helper()
	facts, err := e.facts.GetActiveByField(context.Background(), userID, field)
	if err != nil {
		t.Fatalf("GetActiveByField: %v", err)
	}
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, f.Value)
	}
	return out
}

func (e *consolidateTestEnv) findFact(t *testing.T, userID, field, value string) *model.UserProfileFact {
	t.Helper()
	all, err := e.facts.ListAllByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListAllByUser: %v", err)
	}
	for i := range all {
		if all[i].Field == field && all[i].Value == value {
			return &all[i]
		}
	}
	return nil
}

func TestRewrite_SkipsWhenNoFacts(t *testing.T) {
	env := setupConsolidator(t, &syncMockLLM{})
	res, err := env.c.Rewrite(context.Background(), "u1", RewriteOptions{Force: true})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if !strings.Contains(res.Skipped, "没有生效") {
		t.Errorf("want skip reason about empty profile, got %q", res.Skipped)
	}
}

func TestRewrite_SkipsWhenTooRecent(t *testing.T) {
	env := setupConsolidator(t, &syncMockLLM{})
	ctx := context.Background()
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "常驻地", []string{"北京"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	recent := time.Now().Add(-24 * time.Hour)
	if err := env.state.Save(ctx, &model.ProfileRewriteState{UserID: "u1", LastRunAt: &recent, LastStatus: model.ProfileRewriteStatusOK}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	res, err := env.c.Rewrite(ctx, "u1", RewriteOptions{})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if !strings.Contains(res.Skipped, "不足 7 天") {
		t.Errorf("want 7-day skip, got %q", res.Skipped)
	}
}

func TestRewrite_SkipsWhenActivityLow(t *testing.T) {
	env := setupConsolidator(t, &syncMockLLM{})
	ctx := context.Background()
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "常驻地", []string{"北京"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 上次重写 8 天前（越过首次运行闸门），且库里只有 1 个事件、0 个会话 → 不达门槛
	last := time.Now().Add(-8 * 24 * time.Hour)
	if err := env.state.Save(ctx, &model.ProfileRewriteState{UserID: "u1", LastRunAt: &last, LastStatus: model.ProfileRewriteStatusOK}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if err := env.db.Create(&model.Event{ID: "e1", UserID: "u1", Name: "看房"}).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}
	res, err := env.c.Rewrite(ctx, "u1", RewriteOptions{})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if !strings.Contains(res.Skipped, "活动不足") {
		t.Errorf("want activity skip, got %q", res.Skipped)
	}
}

func TestRewrite_DryRunDoesNotWrite(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"常驻地":["北京"],"硬约束":["已戒酒"]}`}}}
	env := setupConsolidator(t, mock)
	ctx := context.Background()
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "常驻地", []string{"北京"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := env.c.Rewrite(ctx, "u1", RewriteOptions{DryRun: true, Force: true})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if res.Skipped != "" {
		t.Fatalf("dry-run should not skip: %s", res.Skipped)
	}
	if !strings.Contains(res.Diff, "已戒酒") {
		t.Errorf("diff should show new fact, got %q", res.Diff)
	}
	if got := env.activeValues(t, "u1", "硬约束"); len(got) != 0 {
		t.Errorf("dry-run wrote facts: %v", got)
	}
	if st, err := env.state.Get(ctx, "u1"); err != nil || st != nil {
		t.Errorf("dry-run should not save state: state=%v err=%v", st, err)
	}
	if files, _ := filepath.Glob(filepath.Join(env.backupDir, "*.json")); len(files) != 0 {
		t.Errorf("dry-run should not create backup: %v", files)
	}
}

func TestRewrite_AppliesAndSkipsImportantRelations(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"常驻地":["北京"],"硬约束":["已戒酒"],"重要关系":["不该写进来: 关系"]}`}}}
	env := setupConsolidator(t, mock)
	ctx := context.Background()
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "常驻地", []string{"北京"}, "b0"); err != nil {
		t.Fatalf("seed 常驻地: %v", err)
	}
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "重要关系", []string{"CJ: 伴侣"}, "b0"); err != nil {
		t.Fatalf("seed 重要关系: %v", err)
	}

	res, err := env.c.Rewrite(ctx, "u1", RewriteOptions{Force: true})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if res.AppliedFields == 0 {
		t.Fatal("nothing applied")
	}

	if got := env.activeValues(t, "u1", "硬约束"); len(got) != 1 || got[0] != "已戒酒" {
		t.Errorf("硬约束 = %v, want [已戒酒]", got)
	}
	f := env.findFact(t, "u1", "硬约束", "已戒酒")
	if f == nil || f.Source != model.FactSourceRewrite {
		t.Errorf("fact source = %v, want rewrite", f)
	}
	// 重要关系 整字段跳过：保持原值，模型输出被丢弃
	if got := env.activeValues(t, "u1", "重要关系"); len(got) != 1 || got[0] != "CJ: 伴侣" {
		t.Errorf("重要关系 = %v, want [CJ: 伴侣] (untouched)", got)
	}

	st, err := env.state.Get(ctx, "u1")
	if err != nil || st == nil {
		t.Fatalf("state: %v %v", st, err)
	}
	if st.LastRunAt == nil || st.LastStatus != model.ProfileRewriteStatusOK {
		t.Errorf("state = %+v, want ok with last_run_at", st)
	}
	files, _ := filepath.Glob(filepath.Join(env.backupDir, "*.json"))
	if len(files) != 1 {
		t.Fatalf("want exactly 1 backup file, got %v", files)
	}
	if info, err := os.Stat(files[0]); err != nil {
		t.Fatalf("stat backup: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("backup perm = %v, want 0600", info.Mode().Perm())
	}
}

func TestRewrite_ProtectsUserAndRejectedFacts(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"协作偏好":["别写客套话","被删过的条目","新的条目"]}`}}}
	env := setupConsolidator(t, mock)
	ctx := context.Background()

	// 用户手写一条 + 一条被用户删掉的
	if _, err := env.facts.CreateUserFact(ctx, "u1", "协作偏好", "别写客套话"); err != nil {
		t.Fatalf("CreateUserFact: %v", err)
	}
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "协作偏好", []string{"被删过的条目"}, "b0"); err != nil {
		t.Fatalf("seed rejected: %v", err)
	}
	rejected := env.findFact(t, "u1", "协作偏好", "被删过的条目")
	if rejected == nil {
		t.Fatal("seed: rejected fact missing")
	}
	if err := env.facts.RejectFact(ctx, "u1", rejected.ID); err != nil {
		t.Fatalf("RejectFact: %v", err)
	}

	if _, err := env.c.Rewrite(ctx, "u1", RewriteOptions{Force: true}); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}

	user := env.findFact(t, "u1", "协作偏好", "别写客套话")
	if user == nil || user.Source != model.FactSourceUser || user.Status != model.FactStatusActive {
		t.Errorf("user fact = %+v, want source=user status=active", user)
	}
	gone := env.findFact(t, "u1", "协作偏好", "被删过的条目")
	if gone == nil || gone.Status != model.FactStatusRejected {
		t.Errorf("rejected fact = %+v, want still rejected", gone)
	}
	if got := env.activeValues(t, "u1", "协作偏好"); len(got) != 2 {
		t.Errorf("active 协作偏好 = %v, want user fact + 新的条目 only", got)
	}
}

func TestRewrite_RunsWhenDue(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"常驻地":["北京"],"硬约束":["已戒酒"]}`}}}
	env := setupConsolidator(t, mock)
	ctx := context.Background()
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "常驻地", []string{"北京"}, "b0"); err != nil {
		t.Fatalf("seed facts: %v", err)
	}
	// 上次重写 8 天前 → 到期
	last := time.Now().Add(-8 * 24 * time.Hour)
	if err := env.state.Save(ctx, &model.ProfileRewriteState{UserID: "u1", LastRunAt: &last, LastStatus: model.ProfileRewriteStatusOK}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	// 近 7 天：3 个事件（达到门槛）+ 2 个会话（不够门槛，靠事件那条过）
	recent := time.Now().Add(-24 * time.Hour)
	for i, name := range []string{"看房", "面试", "体检"} {
		e := &model.Event{ID: "e" + name, UserID: "u1", Name: name, LastMemoryAt: &recent}
		if err := env.db.Create(e).Error; err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		c := &model.Conversation{ID: "c" + string(rune('a'+i)), UserID: "u1"}
		if err := env.db.Create(c).Error; err != nil {
			t.Fatalf("seed conv %d: %v", i, err)
		}
	}

	res, err := env.c.Rewrite(ctx, "u1", RewriteOptions{})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if res.Skipped != "" {
		t.Fatalf("should run when due, got skip: %s", res.Skipped)
	}
	if res.EventCount != 3 {
		t.Errorf("EventCount = %d, want 3", res.EventCount)
	}
	if res.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", res.SessionCount)
	}
	if got := env.activeValues(t, "u1", "硬约束"); len(got) != 1 || got[0] != "已戒酒" {
		t.Errorf("硬约束 = %v, want [已戒酒]", got)
	}
	st, err := env.state.Get(ctx, "u1")
	if err != nil || st == nil || st.LastRunAt == nil {
		t.Fatalf("state after run: %+v err=%v", st, err)
	}
	if !st.LastRunAt.After(last) {
		t.Errorf("last_run_at not advanced: was %v, now %v", last, st.LastRunAt)
	}
}

func TestRewrite_FirstRunOnlyRecordsBaseline(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: `{"常驻地":["上海"]}`}}}
	env := setupConsolidator(t, mock)
	ctx := context.Background()
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "常驻地", []string{"北京"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 没有状态行 + 不 force → 只记基线，不动画像、不调 LLM
	res, err := env.c.Rewrite(ctx, "u1", RewriteOptions{})
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if !strings.Contains(res.Skipped, "首次运行") {
		t.Errorf("want first-run skip, got %q", res.Skipped)
	}
	if mock.callCount != 0 {
		t.Errorf("first run must not call LLM, calls=%d", mock.callCount)
	}
	if got := env.activeValues(t, "u1", "常驻地"); len(got) != 1 || got[0] != "北京" {
		t.Errorf("常驻地 = %v, want [北京] (untouched)", got)
	}
	st, err := env.state.Get(ctx, "u1")
	if err != nil || st == nil || st.LastRunAt == nil {
		t.Fatalf("baseline state missing: %+v err=%v", st, err)
	}
	if st.LastFactCount != 1 {
		t.Errorf("LastFactCount = %d, want 1", st.LastFactCount)
	}
}

func TestRewrite_RecordsErrorState(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: "这不是 JSON"}}}
	env := setupConsolidator(t, mock)
	ctx := context.Background()
	if err := env.facts.ReplaceFieldFacts(ctx, "u1", "常驻地", []string{"北京"}, "b0"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := env.c.Rewrite(ctx, "u1", RewriteOptions{Force: true}); err == nil {
		t.Fatal("want error on unparseable output")
	}
	st, err := env.state.Get(ctx, "u1")
	if err != nil || st == nil {
		t.Fatalf("state: %v %v", st, err)
	}
	if st.LastStatus != model.ProfileRewriteStatusError || st.LastError == "" {
		t.Errorf("state = %+v, want error status + message", st)
	}
	if st.LastRunAt != nil {
		t.Error("failed rewrite must not advance last_run_at")
	}
}

func TestBuildRewriteDiff(t *testing.T) {
	current := map[string][]string{"常驻地": {"北京"}, "硬约束": {"已戒酒"}}
	rebuilt := map[string][]string{"常驻地": {"北京"}, "硬约束": {"长期尿频"}}
	diff, changed := buildRewriteDiff(current, rebuilt)
	if changed != 1 {
		t.Errorf("changed = %d, want 1（常驻地没变）", changed)
	}
	for _, want := range []string{"[保留] 北京", "[丢弃] 已戒酒", "[新增] 长期尿频"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "重要关系") {
		t.Error("diff should not mention skipped field")
	}
}
