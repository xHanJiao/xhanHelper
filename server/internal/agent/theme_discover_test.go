package agent

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// mockEmbedder 返回固定向量
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (m *mockEmbedder) ModelName() string { return "mock" }

// mockVectorStore 按预设结果返回
type mockVectorStore struct {
	hits []app.VectorMatch
}

func (m *mockVectorStore) Search(query []float64, topK int, filter app.VectorFilter) []app.VectorMatch {
	var results []app.VectorMatch
	for _, h := range m.hits {
		if filter == nil || filter(h.ID, nil, h.Metadata) {
			results = append(results, h)
		}
	}
	return results
}
func (m *mockVectorStore) Upsert(id string, vec []float64, metadata map[string]string) {}
func (m *mockVectorStore) GetVector(id string) ([]float64, bool)                       { return nil, false }
func (m *mockVectorStore) Delete(id string)                                            {}
func (m *mockVectorStore) Size() int                                                   { return 0 }

func setupTestThemeStore(t *testing.T) (*ThemeStore, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.ThemeState{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewThemeStateRepo(db)
	store := NewThemeStore(repo)
	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return store, cleanup
}

func createTestTheme(t *testing.T, store *ThemeStore, userID, name, status string) *model.ThemeState {
	t.Helper()
	theme := &model.ThemeState{
		ID:     name + "-id",
		UserID: userID,
		Name:   name,
		Status: status,
	}
	if err := store.Create(context.Background(), theme); err != nil {
		t.Fatalf("create theme: %v", err)
	}
	return theme
}

func TestHandleExisting_Active(t *testing.T) {
	store, cleanup := setupTestThemeStore(t)
	defer cleanup()
	td := &ThemeDiscoverer{themeStore: store}

	createTestTheme(t, store, "u1", "睡眠追踪", "active")
	existing, _ := store.GetByName(context.Background(), "u1", "睡眠追踪")

	handled, reason := td.handleExisting(context.Background(), "u1", existing)

	if !handled {
		t.Error("expected handled=true for active theme")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestHandleExisting_Closed(t *testing.T) {
	store, cleanup := setupTestThemeStore(t)
	defer cleanup()
	td := &ThemeDiscoverer{themeStore: store}

	createTestTheme(t, store, "u1", "睡眠追踪", "closed")
	existing, _ := store.GetByName(context.Background(), "u1", "睡眠追踪")

	handled, reason := td.handleExisting(context.Background(), "u1", existing)

	if !handled {
		t.Error("expected handled=true for closed theme")
	}
	if reason == "" {
		t.Error("expected non-empty reason for reactivation")
	}

	// 验证 status 已更新为 active
	tm, _ := store.GetByID(context.Background(), existing.ID, "u1")
	if tm == nil || tm.Status != "active" {
		t.Errorf("expected status=active, got %v", tm)
	}
}

func TestHandleExisting_Draft(t *testing.T) {
	store, cleanup := setupTestThemeStore(t)
	defer cleanup()
	td := &ThemeDiscoverer{themeStore: store}

	createTestTheme(t, store, "u1", "编程项目", "draft")
	existing, _ := store.GetByName(context.Background(), "u1", "编程项目")

	handled, _ := td.handleExisting(context.Background(), "u1", existing)

	if !handled {
		t.Error("expected handled=true for legacy draft theme")
	}

	// 验证 draft 已升级为 active
	tm, _ := store.GetByID(context.Background(), existing.ID, "u1")
	if tm == nil || tm.Status != "active" {
		t.Errorf("expected status=active, got %v", tm)
	}
}

func TestDedup_NameMatchActive(t *testing.T) {
	store, cleanup := setupTestThemeStore(t)
	defer cleanup()
	td := &ThemeDiscoverer{themeStore: store}

	createTestTheme(t, store, "u1", "睡眠追踪", "active")

	candidates := []themeCandidate{{Name: "睡眠追踪", Description: "追踪睡眠习惯"}}
	kept, rejected := td.dedup(context.Background(), "u1", candidates, nil)

	if len(kept) != 0 {
		t.Errorf("expected 0 kept, got %d", len(kept))
	}
	if len(rejected) != 1 {
		t.Errorf("expected 1 rejected, got %d", len(rejected))
	}
	if rejected[0].Direction != "睡眠追踪" {
		t.Errorf("expected direction=睡眠追踪, got %s", rejected[0].Direction)
	}
}

func TestDedup_NameMatchClosed(t *testing.T) {
	store, cleanup := setupTestThemeStore(t)
	defer cleanup()
	td := &ThemeDiscoverer{themeStore: store}

	createTestTheme(t, store, "u1", "编程项目", "closed")

	candidates := []themeCandidate{{Name: "编程项目", Description: "学习 Rust"}}
	kept, rejected := td.dedup(context.Background(), "u1", candidates, nil)

	if len(kept) != 0 {
		t.Errorf("expected 0 kept (reactivated), got %d", len(kept))
	}
	if len(rejected) != 1 {
		t.Errorf("expected 1 rejected, got %d", len(rejected))
	}

	// 验证已重新激活
	tm, _ := store.GetByID(context.Background(), "编程项目-id", "u1")
	if tm == nil || tm.Status != "active" {
		t.Error("expected reactivated to active")
	}
}

func TestDedup_NoMatch(t *testing.T) {
	store, cleanup := setupTestThemeStore(t)
	defer cleanup()
	td := &ThemeDiscoverer{themeStore: store}

	createTestTheme(t, store, "u1", "睡眠追踪", "active")

	candidates := []themeCandidate{{Name: "求职准备", Description: "投简历和面试"}}
	kept, rejected := td.dedup(context.Background(), "u1", candidates, nil)

	if len(kept) != 1 {
		t.Errorf("expected 1 kept, got %d", len(kept))
	}
	if len(rejected) != 0 {
		t.Errorf("expected 0 rejected, got %d", len(rejected))
	}
	if kept[0].Name != "求职准备" {
		t.Errorf("expected name=求职准备, got %s", kept[0].Name)
	}
}

// TestDedup_NameReactivateThenVecSkips 验证：候选 A 名称命中 reactivate 后，
// 候选 B 的向量路径不会再重复处理已被 A 激活的主题（通过 skipID）。
// 注：当候选 A reactivate 了某个主题，该主题变为 active。
// 候选 B 的向量搜索命中该 active 主题时会 reject 整个候选 B。
// skipID 在此场景下不直接可见，但确保向量搜索不会对同一主题执行重复 UpdateStatus。
func TestDedup_NameReactivateThenVecActive(t *testing.T) {
	store, cleanup := setupTestThemeStore(t)
	defer cleanup()

	// 候选 A 同名 closed 主题 → name 路径 reactivate
	createTestTheme(t, store, "u1", "编程项目", "closed")
	// 另一个 closed 主题 → 向量搜索会命中
	createTestTheme(t, store, "u1", "Rust学习", "closed")

	td := &ThemeDiscoverer{
		themeStore: store,
		embedder:   &mockEmbedder{},
		vectorStore: &mockVectorStore{hits: []app.VectorMatch{
			{ID: "v1", Score: 0.9, Metadata: map[string]string{
				"type": "theme", "user_id": "u1", "theme_id": "编程项目-id",
			}},
			{ID: "v2", Score: 0.85, Metadata: map[string]string{
				"type": "theme", "user_id": "u1", "theme_id": "Rust学习-id",
			}},
		}},
		dedupThreshold: 0.5,
	}

	candidates := []themeCandidate{
		{Name: "编程项目", Description: "学习 Rust"},
		{Name: "系统设计", Description: "学习系统设计"},
	}
	kept, rejected := td.dedup(context.Background(), "u1", candidates, nil)

	// 候选 A: 名称命中 reactivate → reject
	// 候选 B: 向量搜索命中 "编程项目-id"（已被 A 激活），hasActive=true → reject
	if len(kept) != 0 {
		t.Errorf("expected 0 kept, got %d", len(kept))
	}
	if len(rejected) != 2 {
		t.Fatalf("expected 2 rejected, got %d", len(rejected))
	}

	// 编程项目已 reactivate 为 active
	prog, _ := store.GetByID(context.Background(), "编程项目-id", "u1")
	if prog == nil || prog.Status != "active" {
		t.Errorf("expected 编程项目 active, got %v", prog)
	}
}
