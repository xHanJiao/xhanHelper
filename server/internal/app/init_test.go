package app

import (
	"context"
	"testing"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
)

// mockLLMClient 实现 llm.LLMClient，所有方法可定制
type mockLLMClient struct {
	modelName string
}

func (m *mockLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	ch := make(chan *llm.StreamEvent)
	close(ch)
	return ch, nil
}
func (m *mockLLMClient) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	return llm.Message{}, nil
}
func (m *mockLLMClient) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return base
}
func (m *mockLLMClient) ModelName() string { return m.modelName }

func TestInitDB(t *testing.T) {
	cfg := &config.Config{
		DB: config.DBConfig{
			Driver:   "sqlite",
			Database: ":memory:",
		},
	}

	db := InitDB(cfg)
	if db == nil {
		t.Fatal("InitDB returned nil")
	}

	// 验证 AutoMigrate 之后 key tables 存在
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	defer sqlDB.Close()

	for _, table := range []string{"users", "conversations", "messages", "notes", "bills"} {
		var count int64
		if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count).Error; err != nil {
			t.Errorf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s: expected 1, got %d", table, count)
		}
	}
}

func TestInitRepos(t *testing.T) {
	cfg := &config.Config{
		DB: config.DBConfig{
			Driver:   "sqlite",
			Database: ":memory:",
		},
	}

	db := InitDB(cfg)
	repos := InitRepos(cfg, db)

	if repos == nil {
		t.Fatal("InitRepos returned nil")
	}

	// 用反射兜底：所有 *Repo 字段必须非 nil
	// 这里我们显式检查几个关键字段
	checks := map[string]bool{
		"UserRepo":     repos.User != nil,
		"ConvRepo":     repos.Conversation != nil,
		"MsgRepo":      repos.Message != nil,
		"BillRepo":     repos.Bill != nil,
		"MemRepo":      repos.Mem != nil,
		"NoteRepo":     repos.Note != nil,
		"AgentRepo":    repos.Agent != nil,
		"PromptRepo":   repos.Prompt != nil,
		"ToolRepo":     repos.Tool != nil,
		"Embedding":    repos.Embedding != nil,
		"Mailbox":      repos.Mailbox != nil,
		"ThemeState":   repos.ThemeState != nil,
		"Trace":        repos.Trace != nil,
		"Chunk":        repos.Chunk != nil,
		"ConceptEmbed": repos.ConceptEmbed != nil,
		"PendingFind":  repos.PendingFinding != nil,
		"Commitment":   repos.Commitment != nil,
		"DelayedTask":  repos.DelayedTask != nil,
		"Document":     repos.Document != nil,
		"Version":      repos.Version != nil,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("Repos.%s is nil", name)
		}
	}
}

func TestInitLLM(t *testing.T) {
	cfg := &config.Config{}
	mockFlash := &mockLLMClient{modelName: "flash-mock"}
	mockStrong := &mockLLMClient{modelName: "strong-mock"}

	factory := func(cfg config.LLMConfig) (llm.LLMClient, error) {
		return mockFlash, nil
	}
	strongFactory := func(cfg config.LLMConfig) (llm.LLMClient, error) {
		return mockStrong, nil
	}

	clients := InitLLM(cfg, factory, strongFactory)

	if clients == nil {
		t.Fatal("InitLLM returned nil")
	}
	if clients.Flash == nil {
		t.Error("LLMClients.Flash is nil")
	}
	if clients.Strong == nil {
		t.Error("LLMClients.Strong is nil")
	}
}

func TestInitLLM_NoStrongModel(t *testing.T) {
	cfg := &config.Config{}
	mockFlash := &mockLLMClient{modelName: "flash-mock"}

	factory := func(cfg config.LLMConfig) (llm.LLMClient, error) {
		return mockFlash, nil
	}
	strongFactory := func(cfg config.LLMConfig) (llm.LLMClient, error) {
		return nil, nil // strong 不可用
	}

	clients := InitLLM(cfg, factory, strongFactory)

	if clients.Flash == nil {
		t.Error("LLMClients.Flash is nil")
	}
	if clients.Strong != nil {
		t.Error("LLMClients.Strong should be nil when strong factory returns nil")
	}
}

func TestInitInfra(t *testing.T) {
	cfg := &config.Config{
		DB: config.DBConfig{
			Driver:   "sqlite",
			Database: ":memory:",
		},
	}

	db := InitDB(cfg)
	mockEmbedder := &llm.Embedder{}
	mockVS := &mockVectorStore{}

	infra := InitInfra(cfg, db,
		func(apiKey, baseURL, model string) *llm.Embedder {
			return mockEmbedder
		},
		mockVS,
	)

	if infra == nil {
		t.Fatal("InitInfra returned nil")
	}
	if infra.DB == nil {
		t.Error("Infra.DB is nil")
	}
	if infra.Embedder == nil {
		t.Error("Infra.Embedder is nil")
	}
	if infra.VectorStore == nil {
		t.Error("Infra.VectorStore is nil")
	}
	if infra.DB != db {
		t.Error("Infra.DB should be the same instance passed in")
	}
	if infra.Embedder != mockEmbedder {
		t.Error("Infra.Embedder should be the mock embedder")
	}
}

// mockVectorStore 实现 app.VectorStore 接口
type mockVectorStore struct{}

func (m *mockVectorStore) Upsert(id string, vector []float64, meta map[string]string) {}
func (m *mockVectorStore) Search(vector []float64, topK int, filter VectorFilter) []VectorMatch {
	return nil
}
func (m *mockVectorStore) GetVector(id string) ([]float64, bool) { return nil, false }
func (m *mockVectorStore) Delete(id string)                      {}
func (m *mockVectorStore) Size() int                             { return 0 }
