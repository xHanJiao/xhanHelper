package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func init() { idgen.Init(1) }

// setupProfileFactRepo 创建 sqlite 的 UserProfileFact repo
func setupProfileFactRepo(t *testing.T) (*service.UserProfileFactRepo, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfileFact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewUserProfileFactRepo(db)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return repo, cleanup
}

func seedProfileFacts(t *testing.T, repo *service.UserProfileFactRepo, userID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.ReplaceFieldFacts(ctx, userID, "称呼", []string{"哥"}, "b1"); err != nil {
		t.Fatalf("seed 称呼: %v", err)
	}
	if err := repo.ReplaceFieldFacts(ctx, userID, "籍贯", []string{"河南焦作"}, "b2"); err != nil {
		t.Fatalf("seed 籍贯: %v", err)
	}
	if err := repo.ReplaceFieldFacts(ctx, userID, "职业领域", []string{"5年Go后端", "gap期求职中"}, "b3"); err != nil {
		t.Fatalf("seed 职业领域: %v", err)
	}
}

func execProfileGet(t *testing.T, tool *ProfileTool, userID string, fields []string) *Result {
	t.Helper()
	args, _ := json.Marshal(map[string]interface{}{"fields": fields})
	return tool.Execute(&ExecuteContext{Ctx: context.Background(), UserID: userID}, "get", args)
}

func TestProfile_AllFieldsGrouped(t *testing.T) {
	repo, cleanup := setupProfileFactRepo(t)
	defer cleanup()
	seedProfileFacts(t, repo, "u1")

	tool := NewProfileTool(repo)
	res := execProfileGet(t, tool, "u1", nil)
	if !res.Success {
		t.Fatalf("应成功: %+v", res)
	}
	var out struct {
		Fields map[string]struct {
			Desc  string   `json:"desc"`
			Facts []string `json:"facts"`
		} `json:"fields"`
		IsEmpty bool `json:"is_empty"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("输出应为 JSON: %v\n%s", err, res.Data)
	}
	if len(out.Fields) != 3 {
		t.Errorf("应有 3 个字段, got %d", len(out.Fields))
	}
	if out.IsEmpty {
		t.Error("不应为空")
	}
	if facts := out.Fields["职业领域"].Facts; len(facts) != 2 || facts[0] != "5年Go后端" || facts[1] != "gap期求职中" {
		t.Errorf("职业领域应含 2 条事实: %v", facts)
	}
	if out.Fields["籍贯"].Desc == "" {
		t.Error("字段应带 desc")
	}
}

func TestProfile_FieldsFilter(t *testing.T) {
	repo, cleanup := setupProfileFactRepo(t)
	defer cleanup()
	seedProfileFacts(t, repo, "u1")

	tool := NewProfileTool(repo)
	res := execProfileGet(t, tool, "u1", []string{"称呼"})
	if !res.Success {
		t.Fatalf("应成功: %+v", res)
	}
	var out struct {
		Fields map[string]struct {
			Facts []string `json:"facts"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("输出应为 JSON: %v", err)
	}
	if len(out.Fields) != 1 {
		t.Errorf("过滤后应只 1 字段, got %d", len(out.Fields))
	}
	if _, ok := out.Fields["称呼"]; !ok {
		t.Error("应只含称呼字段")
	}
	if facts := out.Fields["称呼"].Facts; len(facts) != 1 || facts[0] != "哥" {
		t.Errorf("称呼 facts 错误: %v", facts)
	}
}

func TestProfile_InvalidFieldErrors(t *testing.T) {
	repo, cleanup := setupProfileFactRepo(t)
	defer cleanup()
	seedProfileFacts(t, repo, "u1")

	tool := NewProfileTool(repo)
	res := execProfileGet(t, tool, "u1", []string{"不存在的字段"})
	if res.Success {
		t.Fatalf("非法字段应失败: %+v", res)
	}
	if !strings.Contains(res.Error, "未知画像字段") || !strings.Contains(res.Error, "称呼") {
		t.Errorf("错误应列出合法字段: %s", res.Error)
	}
}

func TestProfile_EmptyProfile(t *testing.T) {
	repo, cleanup := setupProfileFactRepo(t)
	defer cleanup()

	tool := NewProfileTool(repo)
	res := execProfileGet(t, tool, "u1", nil)
	if !res.Success {
		t.Fatalf("空画像应成功返回: %+v", res)
	}
	var out struct {
		Fields  map[string]interface{} `json:"fields"`
		IsEmpty bool                   `json:"is_empty"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("输出应为 JSON: %v", err)
	}
	if !out.IsEmpty || len(out.Fields) != 0 {
		t.Errorf("空画像应 is_empty=true, 无字段: %+v", out)
	}
}

func TestProfile_FieldListInDescription(t *testing.T) {
	repo, cleanup := setupProfileFactRepo(t)
	defer cleanup()
	tool := NewProfileTool(repo)
	fns := tool.Functions()
	if len(fns) != 1 {
		t.Fatalf("应有 1 个函数, got %d", len(fns))
	}
	props, ok := fns[0].Parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("参数应为 object: %+v", fns[0].Parameters)
	}
	fieldProp, ok := props["fields"].(map[string]interface{})
	if !ok {
		t.Fatalf("fields 参数缺失: %+v", props)
	}
	desc, _ := fieldProp["description"].(string)
	for _, f := range []string{"称呼", "出生日期", "籍贯", "职业领域", "重要关系", "补充说明"} {
		if !strings.Contains(desc, f) {
			t.Errorf("fields 描述应含字段 %s: %s", f, desc)
		}
	}
}

func TestProfile_UnknownFunction(t *testing.T) {
	repo, cleanup := setupProfileFactRepo(t)
	defer cleanup()
	tool := NewProfileTool(repo)
	res := tool.Execute(&ExecuteContext{Ctx: context.Background(), UserID: "u1"}, "nope", nil)
	if res.Success || !strings.Contains(res.Error, "未知函数") {
		t.Errorf("未知函数应失败: %+v", res)
	}
}

func TestProfile_NilRepoPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("nil factRepo 应 panic")
		}
	}()
	NewProfileTool(nil)
}

func TestProfile_UserIsolation(t *testing.T) {
	repo, cleanup := setupProfileFactRepo(t)
	defer cleanup()
	seedProfileFacts(t, repo, "u1")

	tool := NewProfileTool(repo)
	res := execProfileGet(t, tool, "u2", nil)
	if !res.Success {
		t.Fatalf("u2 应成功: %+v", res)
	}
	var out struct {
		Fields map[string]interface{} `json:"fields"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("输出应为 JSON: %v", err)
	}
	if len(out.Fields) != 0 {
		t.Errorf("u2 不应看到 u1 的画像, got %d", len(out.Fields))
	}
}
