package agent

import (
	"context"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// 摘要 worker：输入完整对话（user + assistant），role 必须标注，空 content 跳过。
func TestSummaryWorker_CallFlashSendsFullDialog(t *testing.T) {
	mock := &syncMockLLM{responses: []llm.Message{{Content: "讨论了方案选型；倾向方案B"}}}
	w := NewSummaryWorker(&SummaryWorkerConfig{FlashClient: mock})

	got, _, _, err := w.callFlash(context.Background(), "", []model.Message{
		{ID: "m1", Role: "user", Content: "方案A和B哪个好"},
		{ID: "m2", Role: "assistant", Content: "建议B，成本更低"},
		{ID: "m3", Role: "assistant", Content: ""}, // 纯 tool call 轮
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "方案B") {
		t.Errorf("unexpected summary: %q", got)
	}
	if mock.reqs[0].SystemPrompt != postChatDialogSystemPrompt {
		t.Error("should use the dialog system prompt")
	}
	content := mock.reqs[0].Messages[0].Content
	if !strings.Contains(content, `<turn role="assistant"`) || !strings.Contains(content, "建议B") {
		t.Errorf("assistant turn must be present and labeled, got: %.400s", content)
	}
	if strings.Count(content, `<turn role="assistant"`) != 1 {
		t.Errorf("empty turn should be skipped, got: %.400s", content)
	}
}
