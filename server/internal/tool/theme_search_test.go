package tool

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupThemeSearchTool(t *testing.T) (*ThemeSearchTool, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ThemeState{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewThemeStateRepo(db)
	return NewThemeSearchTool(repo, nil, nil), func() {}
}

func seedActiveThemes(t *testing.T, repo *service.ThemeStateRepo, userID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		th := model.ThemeState{
			ID:          "th-" + string(rune('a'+i)),
			UserID:      userID,
			Name:        "主题" + string(rune('a'+i)),
			Description: "描述" + string(rune('a'+i)),
			Status:      "active",
			StateText:   "状态文本" + string(rune('a'+i)),
		}
		if err := repo.Create(context.Background(), &th); err != nil {
			t.Fatalf("seed theme: %v", err)
		}
	}
}

func TestListActiveThemes_DefaultLimit20(t *testing.T) {
	tool, cleanup := setupThemeSearchTool(t)
	defer cleanup()
	seedActiveThemes(t, tool.themeRepo, "u1", 30)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "list_active_themes", mustMarshal(t, map[string]interface{}{}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	themes := out["themes"].([]interface{})
	if len(themes) != 20 {
		t.Fatalf("default limit should be 20, got %d", len(themes))
	}
	// 简表：不含 description/state_text
	first := themes[0].(map[string]interface{})
	if _, ok := first["description"]; ok {
		t.Error("brief list should not include description")
	}
	if _, ok := first["state_text"]; ok {
		t.Error("brief list should not include state_text")
	}
	if _, ok := first["name"]; !ok {
		t.Error("brief list should include name")
	}
	if _, ok := first["status"]; !ok {
		t.Error("brief list should include status")
	}
}

func TestListActiveThemes_LimitParam(t *testing.T) {
	tool, cleanup := setupThemeSearchTool(t)
	defer cleanup()
	seedActiveThemes(t, tool.themeRepo, "u1", 30)
	ec := &ExecuteContext{Ctx: context.Background(), UserID: "u1"}

	res := tool.Execute(ec, "list_active_themes", mustMarshal(t, map[string]interface{}{"limit": 5}))
	if !res.Success {
		t.Fatalf("query failed: %s", res.Error)
	}
	var out map[string]interface{}
	json.Unmarshal(res.Data, &out)
	if len(out["themes"].([]interface{})) != 5 {
		t.Errorf("limit=5 should return 5, got %d", len(out["themes"].([]interface{})))
	}
}
