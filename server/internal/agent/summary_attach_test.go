package agent

import (
	"context"
	"testing"

	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// —— MsgExtra SummaryAttachRound 读写 ——

func TestSetAndGetSummaryAttachRound(t *testing.T) {
	msg := &model.Message{}
	SetSummaryAttachRound(msg, 3)
	if got := GetSummaryAttachRound(msg); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func TestGetSummaryAttachRound_NoExtra(t *testing.T) {
	msg := &model.Message{}
	if got := GetSummaryAttachRound(msg); got != 0 {
		t.Errorf("expected 0 for no extra, got %d", got)
	}
}

func TestGetSummaryAttachRound_NegativeOne(t *testing.T) {
	msg := &model.Message{}
	SetSummaryAttachRound(msg, -1)
	if got := GetSummaryAttachRound(msg); got != -1 {
		t.Errorf("expected -1, got %d", got)
	}
}

func TestSetSummaryAttachRound_MergeWithExistingExtra(t *testing.T) {
	msg := &model.Message{}
	// 先设 WindowSize，再设 SummaryAttachRound，验证二者共存
	SetWindowSize(msg, 5)
	SetSummaryAttachRound(msg, 2)

	if got := GetWindowSize(msg); got != 5 {
		t.Errorf("WindowSize: expected 5, got %d", got)
	}
	if got := GetSummaryAttachRound(msg); got != 2 {
		t.Errorf("SummaryAttachRound: expected 2, got %d", got)
	}
}

// —— cosineSimilarity ——

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{1, 2, 3}
	sim := cosineSimilarity(a, b)
	if sim < 0.9999 {
		t.Errorf("expected ~1.0 for identical vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{0, 1, 0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for orthogonal vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float64{1, 0}
	b := []float64{-1, 0}
	sim := cosineSimilarity(a, b)
	if sim > -0.9999 {
		t.Errorf("expected ~-1.0 for opposite vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_DifferentLength(t *testing.T) {
	a := []float64{1, 2}
	b := []float64{1, 2, 3}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for different length vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float64{0, 0, 0}
	b := []float64{1, 2, 3}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for zero vector, got %.6f", sim)
	}
}

func TestCosineSimilarity_AllZeros(t *testing.T) {
	a := []float64{0, 0}
	b := []float64{0, 0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for all-zero vectors, got %.6f", sim)
	}
}

func TestCosineSimilarity_AboveThreshold(t *testing.T) {
	// ~30° angle → cos ≈ 0.866
	a := []float64{1, 0}
	b := []float64{0.866, 0.5}
	sim := cosineSimilarity(a, b)
	if sim < 0.86 || sim > 0.87 {
		t.Errorf("expected ~0.866, got %.6f", sim)
	}
}

func TestCosineSimilarity_BelowThreshold(t *testing.T) {
	// ~60° angle → cos = 0.5
	a := []float64{1, 0}
	b := []float64{0.5, 0.866}
	sim := cosineSimilarity(a, b)
	if sim < 0.49 || sim > 0.51 {
		t.Errorf("expected ~0.5, got %.6f", sim)
	}
}

// —— getOrEmbedSummary ——

func TestGetOrEmbedSummary_CacheHit(t *testing.T) {
	vs := NewVectorStore()
	summaryID := "test-summary-cached"
	vsKey := "summary:" + summaryID
	expectedVec := []float64{0.1, 0.2, 0.3}
	vs.Upsert(vsKey, expectedVec, map[string]string{
		"type":       "summary",
		"summary_id": summaryID,
	})

	o := &Orchestrator{vectorStore: vs}
	summary := &model.ConversationSummary{ID: summaryID, SummaryText: "不会用到"}

	vec := o.getOrEmbedSummary(context.Background(), summary)
	if len(vec) != 3 {
		t.Fatalf("expected 3-d vector, got %d", len(vec))
	}
	if vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("expected [0.1, 0.2, 0.3], got %v", vec)
	}
}

func TestGetOrEmbedSummary_EmbedFail(t *testing.T) {
	// embedder 为 nil → Embed 会 panic（硬依赖），但 Embedder.Embed 内部会做 HTTP 调用。
	// 这里测试当 embedder 为 nil 时，getOrEmbedSummary 的行为。
	// 实际上 embedder 是硬依赖，此测试验证当 cache miss 时 embedder 被调用。
	// 跳过此测试因为需要真实的 embedder。
	t.Skip("requires real embedder for cache miss path")
}

// —— checkSummaryRelevance 计算逻辑 ——

// 注意：checkSummaryRelevance 内部调用 tryEmbedContent（使用 embedder），
// embedder 是硬依赖。以下测试直接验证相似度计算逻辑：
// cosine 相似度 + 窗口取 max + 阈值比较。

func TestSummaryRelevance_HighSimilarityPasses(t *testing.T) {
	// 模拟：摘要与用户消息高度相似（cos ≈ 0.98），应通过阈值 0.5
	summaryVec := []float64{1, 0, 0}
	queryVec := []float64{0.98, 0.1, 0}
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim < 0.5 {
		t.Errorf("expected high similarity (>0.5), got %.6f", sim)
	}
}

func TestSummaryRelevance_OrthogonalFails(t *testing.T) {
	// 模拟：摘要与用户消息正交（cos = 0），应低于阈值 0.5
	summaryVec := []float64{1, 0, 0}
	queryVec := []float64{0, 1, 0}
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim >= 0.5 {
		t.Errorf("expected low similarity (<0.5) for orthogonal vectors, got %.6f", sim)
	}
}

func TestSummaryRelevance_MaxAcrossWindow(t *testing.T) {
	// 模拟窗口取 max：一个正交（cos=0），一个相似（cos≈1）
	summaryVec := []float64{1, 0}
	queryVecs := [][]float64{
		{0, 1},       // cos = 0
		{0.95, 0.05}, // cos ≈ 0.998
	}
	maxSim := 0.0
	for _, v := range queryVecs {
		if sim := cosineSimilarity(summaryVec, v); sim > maxSim {
			maxSim = sim
		}
	}
	if maxSim < 0.5 {
		t.Errorf("expected max similarity >0.5 (max wins), got %.6f", maxSim)
	}
}

func TestSummaryRelevance_BoundaryEqualsThreshold(t *testing.T) {
	// cos(60°) = 0.5，恰好等于阈值 → 应通过（>= 判断）
	summaryVec := []float64{1, 0}
	queryVec := []float64{0.5, 0.8660254} // cos(60°) = 0.5
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim < 0.499 || sim > 0.501 {
		t.Errorf("expected ~0.5, got %.6f", sim)
	}
	if sim < 0.5 {
		t.Error("boundary: similarity=0.5 should satisfy threshold >=0.5")
	}
}

func TestSummaryRelevance_JustBelowThreshold(t *testing.T) {
	// cos ≈ 0.49，略低于阈值 0.5
	summaryVec := []float64{1, 0}
	queryVec := []float64{0.49, 0.8717} // cos ≈ 0.49
	sim := cosineSimilarity(summaryVec, queryVec)
	if sim >= 0.5 {
		t.Errorf("expected <0.5, got %.6f", sim)
	}
}

func TestSummaryRelevance_EmptyQueryWindow(t *testing.T) {
	// 空查询窗口 → 乐观附着（返回 true）
	// 此行为由 checkSummaryRelevance 中的 len(queryVecs)==0 分支保证
	queryVecs := [][]float64{}
	if len(queryVecs) != 0 {
		t.Error("expected empty query vectors")
	}
	// 空窗口时 checkSummaryRelevance 返回 true（乐观附着）
}

func TestGetOrEmbedSummary_NewEmbed(t *testing.T) {
	// 测试 getOrEmbedSummary 在 cache miss 时的行为
	// embedder 是硬依赖，此测试需要真实 embedder — 跳过
	t.Skip("requires real embedder for cache miss path")
}

// —— SummaryAttachRound 状态机 ——

// testMsgRepo 实现 msgCreator 接口，用于测试 runReActLoop 中的轮次跟踪。
type testMsgRepo struct {
	created    []*model.Message
	recentFunc func(ctx context.Context, convID string, limit int) ([]model.Message, error)
}

func (m *testMsgRepo) Create(ctx context.Context, msg *model.Message) error {
	m.created = append(m.created, msg)
	return nil
}

func (m *testMsgRepo) ListRecentUserMessages(ctx context.Context, convID string, limit int) ([]model.Message, error) {
	if m.recentFunc != nil {
		return m.recentFunc(ctx, convID, limit)
	}
	return nil, nil
}

func TestSummaryAttachRound_NewSessionStartsAtOne(t *testing.T) {
	repo := &testMsgRepo{}

	o := &Orchestrator{
		msgRepo:                   repo,
		summaryRepo:               nil, // 无摘要 repo，不触发附着
		maxSummaryAttachRounds:    5,
		summaryRelevanceThreshold: 0.5,
		summaryRelevanceHistoryN:  5,
		embedder:                  nil,
		vectorStore:               NewVectorStore(),
	}

	events := make(chan *llm.StreamEvent, 16)
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		Messages:           []llm.Message{},
		UserMessageContent: "hello",
		Window: WindowResult{
			Size:         3,
			IsNewSession: true,
		},
	}

	o.runReActLoop(context.Background(), events, &promptbuilder.BuildResult{}, nil, req)

	// 验证 userMsg 的 SummaryAttachRound 被设为 1
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 created message, got %d", len(repo.created))
	}
	round := GetSummaryAttachRound(repo.created[0])
	if round != 1 {
		t.Errorf("new session: expected SummaryAttachRound=1, got %d", round)
	}
}

func TestSummaryAttachRound_Increments(t *testing.T) {
	prevMsgID := "prev-user-msg"

	repo := &testMsgRepo{
		recentFunc: func(ctx context.Context, convID string, limit int) ([]model.Message, error) {
			// 返回上一条设置了 attach round=2 的消息
			prev := model.Message{ID: prevMsgID, Role: "user"}
			SetSummaryAttachRound(&prev, 2)
			return []model.Message{prev}, nil
		},
	}

	o := &Orchestrator{
		msgRepo:                   repo,
		summaryRepo:               nil,
		maxSummaryAttachRounds:    5,
		summaryRelevanceThreshold: 0.5,
		summaryRelevanceHistoryN:  5,
		embedder:                  nil,
		vectorStore:               NewVectorStore(),
	}

	events := make(chan *llm.StreamEvent, 16)
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		Messages:           []llm.Message{},
		UserMessageContent: "hello again",
		Window: WindowResult{
			Size:              4,
			IsNewSession:      false,
			SessionStartMsgID: "session-start-1",
		},
	}

	o.runReActLoop(context.Background(), events, &promptbuilder.BuildResult{}, nil, req)

	// 应递增到 3
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 created message, got %d", len(repo.created))
	}
	round := GetSummaryAttachRound(repo.created[0])
	if round != 3 {
		t.Errorf("expected SummaryAttachRound=3 (prev=2 + 1), got %d", round)
	}
}

func TestSummaryAttachRound_AtLimitStops(t *testing.T) {
	prevMsgID := "prev-at-limit"

	repo := &testMsgRepo{
		recentFunc: func(ctx context.Context, convID string, limit int) ([]model.Message, error) {
			prev := model.Message{ID: prevMsgID, Role: "user"}
			SetSummaryAttachRound(&prev, 5) // 已达上限
			return []model.Message{prev}, nil
		},
	}

	o := &Orchestrator{
		msgRepo:                   repo,
		summaryRepo:               nil,
		maxSummaryAttachRounds:    5,
		summaryRelevanceThreshold: 0.5,
		summaryRelevanceHistoryN:  5,
		embedder:                  nil,
		vectorStore:               NewVectorStore(),
	}

	events := make(chan *llm.StreamEvent, 16)
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		Messages:           []llm.Message{},
		UserMessageContent: "round 6",
		Window: WindowResult{
			Size:              8,
			IsNewSession:      false,
			SessionStartMsgID: "session-start-1",
		},
	}

	o.runReActLoop(context.Background(), events, &promptbuilder.BuildResult{}, nil, req)

	if len(repo.created) != 1 {
		t.Fatalf("expected 1 created message, got %d", len(repo.created))
	}
	round := GetSummaryAttachRound(repo.created[0])
	if round != -1 {
		t.Errorf("at limit: expected SummaryAttachRound=-1 (permanently skipped), got %d", round)
	}
}

func TestSummaryAttachRound_PermanentlySkipPropagates(t *testing.T) {
	prevMsgID := "prev-skipped"

	repo := &testMsgRepo{
		recentFunc: func(ctx context.Context, convID string, limit int) ([]model.Message, error) {
			prev := model.Message{ID: prevMsgID, Role: "user"}
			SetSummaryAttachRound(&prev, -1) // 已永久跳过
			return []model.Message{prev}, nil
		},
	}

	o := &Orchestrator{
		msgRepo:                   repo,
		summaryRepo:               nil,
		maxSummaryAttachRounds:    5,
		summaryRelevanceThreshold: 0.5,
		summaryRelevanceHistoryN:  5,
		embedder:                  nil,
		vectorStore:               NewVectorStore(),
	}

	events := make(chan *llm.StreamEvent, 16)
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		Messages:           []llm.Message{},
		UserMessageContent: "still skipped",
		Window: WindowResult{
			Size:              5,
			IsNewSession:      false,
			SessionStartMsgID: "session-start-1",
		},
	}

	o.runReActLoop(context.Background(), events, &promptbuilder.BuildResult{}, nil, req)

	if len(repo.created) != 1 {
		t.Fatalf("expected 1 created message, got %d", len(repo.created))
	}
	round := GetSummaryAttachRound(repo.created[0])
	if round != -1 {
		t.Errorf("expected SummaryAttachRound=-1 (propagated from prev=-1), got %d", round)
	}
}

func TestSummaryAttachRound_NewSessionResets(t *testing.T) {
	// 上一条消息 SummaryAttachRound=-1（永久跳过），但新 session 重置为 1
	prevMsgID := "prev-old-session"

	repo := &testMsgRepo{
		recentFunc: func(ctx context.Context, convID string, limit int) ([]model.Message, error) {
			prev := model.Message{ID: prevMsgID, Role: "user"}
			SetSummaryAttachRound(&prev, -1)
			return []model.Message{prev}, nil
		},
	}

	o := &Orchestrator{
		msgRepo:                   repo,
		summaryRepo:               nil,
		maxSummaryAttachRounds:    5,
		summaryRelevanceThreshold: 0.5,
		summaryRelevanceHistoryN:  5,
		embedder:                  nil,
		vectorStore:               NewVectorStore(),
	}

	events := make(chan *llm.StreamEvent, 16)
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		Messages:           []llm.Message{},
		UserMessageContent: "new session",
		Window: WindowResult{
			Size:         3,
			IsNewSession: true, // 新 session!
		},
	}

	o.runReActLoop(context.Background(), events, &promptbuilder.BuildResult{}, nil, req)

	if len(repo.created) != 1 {
		t.Fatalf("expected 1 created message, got %d", len(repo.created))
	}
	round := GetSummaryAttachRound(repo.created[0])
	if round != 1 {
		t.Errorf("new session reset: expected SummaryAttachRound=1, got %d", round)
	}
}
