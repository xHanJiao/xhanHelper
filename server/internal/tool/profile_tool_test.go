package tool

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupProfileTool(t *testing.T) (*ProfileTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfile{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewUserProfileRepo(db)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return NewProfileTool(repo), cleanup
}

func TestProfileTool_UpdateField(t *testing.T) {
	tool, cleanup := setupProfileTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "update_profile_field", mustMarshal(t, map[string]interface{}{
		"field": "籍贯", "value": "河南焦作",
	}))
	if !res.Success {
		t.Fatalf("update should succeed: %s", res.Error)
	}

	p, err := tool.profileRepo.GetByUserAndField(context.Background(), "u1", "籍贯")
	if err != nil {
		t.Fatalf("GetByUserAndField: %v", err)
	}
	if p == nil || p.FieldValue != "河南焦作" {
		t.Fatalf("expected 籍贯=河南焦作, got %+v", p)
	}
	if p.Source != model.ProfileSourceExplicit {
		t.Errorf("expected source=explicit, got %s", p.Source)
	}
}

func TestProfileTool_UpdateField_Overwrite(t *testing.T) {
	tool, cleanup := setupProfileTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	if res := tool.Execute(ec, "update_profile_field", mustMarshal(t, map[string]interface{}{"field": "籍贯", "value": "湖南"})); !res.Success {
		t.Fatalf("first update: %s", res.Error)
	}
	if res := tool.Execute(ec, "update_profile_field", mustMarshal(t, map[string]interface{}{"field": "籍贯", "value": "郑州"})); !res.Success {
		t.Fatalf("second update: %s", res.Error)
	}
	p, _ := tool.profileRepo.GetByUserAndField(context.Background(), "u1", "籍贯")
	if p.FieldValue != "郑州" {
		t.Errorf("expected overwrite to 郑州, got %s", p.FieldValue)
	}
}

func TestProfileTool_UpdateField_NonWhitelistRejected(t *testing.T) {
	tool, cleanup := setupProfileTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "update_profile_field", mustMarshal(t, map[string]interface{}{
		"field": "不存在的字段", "value": "xxx",
	}))
	if res.Success {
		t.Fatal("non-whitelist field should be rejected")
	}
	hasAny, _ := tool.profileRepo.HasAny(context.Background(), "u1")
	if hasAny {
		t.Error("rejected field should not be written")
	}
}

func TestProfileTool_UpdateField_EmptyValueRejected(t *testing.T) {
	tool, cleanup := setupProfileTool(t)
	defer cleanup()
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	for _, v := range []string{"", "未知", "无", "暂无"} {
		res := tool.Execute(ec, "update_profile_field", mustMarshal(t, map[string]interface{}{
			"field": "籍贯", "value": v,
		}))
		if res.Success {
			t.Fatalf("value %q should be rejected", v)
		}
	}
	hasAny, _ := tool.profileRepo.HasAny(context.Background(), "u1")
	if hasAny {
		t.Error("placeholder value should not be written")
	}
}

func TestProfileTool_UpdateField_UserScoped(t *testing.T) {
	tool, cleanup := setupProfileTool(t)
	defer cleanup()

	ec1 := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}
	ec2 := &ExecuteContext{Ctx: context.Background(), UserID: "u2"}
	if res := tool.Execute(ec1, "update_profile_field", mustMarshal(t, map[string]interface{}{"field": "籍贯", "value": "河南焦作"})); !res.Success {
		t.Fatalf("u1 update: %s", res.Error)
	}
	if res := tool.Execute(ec2, "update_profile_field", mustMarshal(t, map[string]interface{}{"field": "籍贯", "value": "上海"})); !res.Success {
		t.Fatalf("u2 update: %s", res.Error)
	}

	p1, _ := tool.profileRepo.GetByUserAndField(context.Background(), "u1", "籍贯")
	p2, _ := tool.profileRepo.GetByUserAndField(context.Background(), "u2", "籍贯")
	if p1.FieldValue != "河南焦作" || p2.FieldValue != "上海" {
		t.Errorf("user scoping broken: u1=%s u2=%s", p1.FieldValue, p2.FieldValue)
	}
}
