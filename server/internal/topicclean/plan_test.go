package topicclean

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func TestValidateNewTopic(t *testing.T) {
	ok := []string{"睡眠", "睡眠管理", "网页渲染", "项目管理", "AI工具", "OK", "Swift", "26计划"}
	for _, s := range ok {
		if err := validateNewTopic(s); err != nil {
			t.Errorf("%q should be valid: %v", s, err)
		}
	}
	bad := []string{"", "   ", "六个汉字呀啊", "a/b", "a,b", "a b", "a、b", "求职-面试"}
	for _, s := range bad {
		if err := validateNewTopic(s); err == nil {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestFindViolations(t *testing.T) {
	topics := []topicRef{
		{Topic: "睡眠", Count: 3},
		{Topic: "项目规划", Count: 5},
		{Topic: "AI工具", Count: 4}, // 中英文数字混合 OK
		{Topic: "OK", Count: 2},
		{Topic: "", Count: 1},
		{Topic: "WKWebView性能调优", Count: 2}, // 超 5 字 → 违规
		{Topic: "求职-面试", Count: 4},         // 含标点 → 违规
	}
	got := findViolations(topics)
	want := map[string]bool{"": true, "WKWebView性能调优": true, "求职-面试": true}
	if len(got) != len(want) {
		t.Fatalf("want %d violations, got %d: %+v", len(want), len(got), got)
	}
	for _, v := range got {
		if !want[v.OldTopic] {
			t.Errorf("%q should not be a violation", v.OldTopic)
		}
	}
}

func TestParseAndRenderPlan(t *testing.T) {
	violations := []Violation{
		{OldTopic: "WKWebView性能调优", Runes: 11, NewTopic: "网页渲染", Action: "rename"},
		{OldTopic: "", Runes: 0, NewTopic: "", Action: "rename"}, // 无候选 → render 应降级为 skip
	}
	plan := renderPlan("u1", violations)
	uid, rows, err := parsePlan(plan)
	if err != nil {
		t.Fatalf("parse own render failed: %v", err)
	}
	if uid != "u1" {
		t.Errorf("user id roundtrip failed: %q", uid)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Action != "rename" || rows[0].NewTopic != "网页渲染" {
		t.Errorf("row0 wrong: %+v", rows[0])
	}
	if rows[1].Action != "skip" {
		t.Errorf("empty-candidate row must render as skip, got %+v", rows[1])
	}
}

func TestParsePlanMissingUser(t *testing.T) {
	if _, _, err := parsePlan("old\tnew\trename\n"); err == nil {
		t.Error("missing user_id line must error")
	}
}

// Generate → Apply 端到端（SQLite，无 LLM）：枚举用户、挑违规、产出映射、dry-run 改名。
func TestGenerateAndApply_SQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}, &model.Note{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	memRepo := service.NewUserMemoryRepo(db)
	noteRepo := service.NewNoteRepo(db)
	if err := memRepo.Create(context.Background(), &model.UserMemory{
		ID: "m1", UserID: "u1", MemoryKey: "WKWebView性能调优", ContentHash: "h1",
		MemoryValue: "v", Topic: "WKWebView性能调优",
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	if err := noteRepo.Create(context.Background(), &model.Note{
		ID: "n1", UserID: "u1", Topic: "太长的笔记主题词啊", Content: "c",
	}); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	_ = memRepo
	_ = noteRepo

	dir := t.TempDir()
	// Generate（no LLM → candidate 留空、action 降级 skip）
	if err := Generate(context.Background(), db, Options{OutDir: dir}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	plan, err := os.ReadFile(filepath.Join(dir, "plan-u1.tsv"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if !strings.Contains(string(plan), "WKWebView性能调优") || !strings.Contains(string(plan), "太长的笔记主题词啊") {
		t.Fatalf("plan missing violations:\n%s", plan)
	}

	// 人工把两行改成 rename + 合规新名，落库
	fixed := strings.ReplaceAll(string(plan), "\t\tskip\t\n", "\t网页性能\trename\t\n")
	if err := os.WriteFile(filepath.Join(dir, "plan-u1.tsv"), []byte(fixed), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	sum, err := Apply(context.Background(), db, Options{OutDir: dir, DryRun: false})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if sum.MemoriesRenamed != 1 || sum.NotesRenamed != 1 {
		t.Fatalf("rename counts wrong: %+v", sum)
	}
	mem, _ := memRepo.FindByID(context.Background(), "u1", "m1")
	if mem.Topic != "网页性能" {
		t.Errorf("memory topic not renamed: %q", mem.Topic)
	}
}

// 空 topic：Generate 逐条列出（带 memory_id），Apply 按 id 填名（NULL/空串都覆盖）。
func TestGenerateAndApply_EmptyTopic(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}, &model.Note{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	memRepo := service.NewUserMemoryRepo(db)
	// 两条空 topic（一条 NULL 风格用 ""，一条纯空白）。
	if err := memRepo.Create(context.Background(), &model.UserMemory{
		ID: "e1", UserID: "u1", MemoryKey: "k", ContentHash: "h1", MemoryValue: "记忆一", Topic: "",
	}); err != nil {
		t.Fatalf("seed e1: %v", err)
	}
	if err := memRepo.Create(context.Background(), &model.UserMemory{
		ID: "e2", UserID: "u1", MemoryKey: "k", ContentHash: "h2", MemoryValue: "记忆二", Topic: "   ",
	}); err != nil {
		t.Fatalf("seed e2: %v", err)
	}

	dir := t.TempDir()
	if err := Generate(context.Background(), db, Options{OutDir: dir}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	plan, err := os.ReadFile(filepath.Join(dir, "plan-u1.tsv"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	// 两条空 topic 各成一行，且带 memory_id。
	if !strings.Contains(string(plan), "\te1") || !strings.Contains(string(plan), "\te2") {
		t.Fatalf("plan missing empty-topic rows with ids:\n%s", plan)
	}

	// 人工填名（action 从 skip 改 rename）。
	fixed := strings.ReplaceAll(string(plan), "\t\tskip\te1", "\t财务\trename\te1")
	fixed = strings.ReplaceAll(fixed, "\t\tskip\te2", "\t健康\trename\te2")
	if err := os.WriteFile(filepath.Join(dir, "plan-u1.tsv"), []byte(fixed), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	sum, err := Apply(context.Background(), db, Options{OutDir: dir, DryRun: false})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if sum.MemoriesRenamed != 2 {
		t.Fatalf("expected 2 memories renamed, got %+v", sum)
	}
	e1, _ := memRepo.FindByID(context.Background(), "u1", "e1")
	e2, _ := memRepo.FindByID(context.Background(), "u1", "e2")
	if e1.Topic != "财务" || e2.Topic != "健康" {
		t.Errorf("empty topics not filled: e1=%q e2=%q", e1.Topic, e2.Topic)
	}
}

// --user 过滤：目录里有多个用户 plan 时，Apply 只处理匹配的那个，绝不误改其他用户。
func TestApplyUserFilter(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}, &model.Note{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	memRepo := service.NewUserMemoryRepo(db)
	for _, u := range []string{"aaaaaaaa", "bbbbbbbb"} {
		if err := memRepo.Create(context.Background(), &model.UserMemory{
			ID: "m-" + u, UserID: u, MemoryKey: "WKWebView性能调优", ContentHash: "h-" + u,
			MemoryValue: "v", Topic: "WKWebView性能调优",
		}); err != nil {
			t.Fatalf("seed %s: %v", u, err)
		}
	}
	dir := t.TempDir()
	if err := Generate(context.Background(), db, Options{OutDir: dir}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, u := range []string{"aaaaaaaa", "bbbbbbbb"} {
		p := filepath.Join(dir, "plan-"+u+".tsv")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		fixed := strings.ReplaceAll(string(b), "\t\tskip\t\n", "\t网页性能\trename\t\n")
		if err := os.WriteFile(p, []byte(fixed), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	// 只处理 aaaaaaaa
	if _, err := Apply(context.Background(), db, Options{OutDir: dir, DryRun: false, UserID: "aaaaa"}); err != nil {
		t.Fatalf("apply --user: %v", err)
	}
	a, _ := memRepo.FindByID(context.Background(), "aaaaaaaa", "m-aaaaaaaa")
	b, _ := memRepo.FindByID(context.Background(), "bbbbbbbb", "m-bbbbbbbb")
	if a.Topic != "网页性能" {
		t.Errorf("matching user not renamed: %q", a.Topic)
	}
	if b.Topic != "WKWebView性能调优" {
		t.Errorf("other user must be untouched, got %q", b.Topic)
	}

	// --user 无对应 plan → 报错，不误改
	if _, err := Apply(context.Background(), db, Options{OutDir: dir, UserID: "cccccccc"}); err == nil {
		t.Errorf("unmatched --user must error, not silently apply all")
	}
}
