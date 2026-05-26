package retrieval

import (
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/model"
)

func TestFilterMessageRound(t *testing.T) {
	now := time.Now()
	m1 := model.Message{ID: "m1", Role: "user", Content: "帮我查账单", CreatedAt: now}
	m2 := model.Message{ID: "m2", Role: "assistant", Content: "好的，我来查", CreatedAt: now.Add(time.Second)}
	m3 := model.Message{ID: "m3", Role: "tool", Content: `{"result":"ok"}`, CreatedAt: now.Add(2 * time.Second), IsInternal: true}
	m4 := model.Message{ID: "m4", Role: "assistant", Content: "你的账单如下...", CreatedAt: now.Add(3 * time.Second)}
	m5 := model.Message{ID: "m5", Role: "user", Content: "谢谢", CreatedAt: now.Add(4 * time.Second)}

	t.Run("filters tool and internal messages", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m3, m4}
		got := FilterMessageRound(msgs, "m1")
		if len(got) != 3 {
			t.Errorf("expected 3 messages, got %d", len(got))
		}
		for _, m := range got {
			if m.Role == "tool" || m.IsInternal {
				t.Errorf("tool/internal message should be filtered: %v", m)
			}
		}
	})

	t.Run("stops at next user message", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m5}
		got := FilterMessageRound(msgs, "m1")
		if len(got) != 2 {
			t.Errorf("expected 2 messages (stopped at next user), got %d", len(got))
		}
		if got[1].ID != "m2" {
			t.Errorf("expected assistant msg, got %s", got[1].ID)
		}
	})

	t.Run("full round chain", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m3, m4, m5}
		got := FilterMessageRound(msgs, "m1")
		if len(got) != 3 {
			t.Errorf("expected 3 messages, got %d", len(got))
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		got := FilterMessageRound(nil, "m1")
		if len(got) != 0 {
			t.Errorf("expected empty, got %d", len(got))
		}
	})

	t.Run("skip empty content", func(t *testing.T) {
		mEmpty := model.Message{ID: "me", Role: "assistant", Content: "", CreatedAt: now}
		got := FilterMessageRound([]model.Message{m1, mEmpty}, "m1")
		if len(got) != 1 {
			t.Errorf("expected 1 message (empty content skipped), got %d", len(got))
		}
	})
}

func TestFormatMessageRound(t *testing.T) {
	now := time.Now()
	msgs := []model.Message{
		{ID: "m1", ConversationID: "conv1", Role: "user", Content: "帮我查账单", CreatedAt: now},
		{ID: "m2", ConversationID: "conv1", Role: "assistant", Content: "你的账单如下：共计342元", CreatedAt: now.Add(time.Second)},
	}

	result := FormatMessageRound(msgs, "测试会话")
	if !strings.Contains(result, "测试会话") {
		t.Errorf("expected result to contain conversation title, got: %s", result)
	}
	if !strings.Contains(result, "用户:") && !strings.Contains(result, "用户：") {
		t.Errorf("expected result to contain user indicator, got: %s", result)
	}
	if !strings.Contains(result, "助手:") && !strings.Contains(result, "助手：") {
		t.Errorf("expected result to contain assistant indicator, got: %s", result)
	}
	if !strings.Contains(result, "帮我查账单") {
		t.Errorf("expected result to contain user message content, got: %s", result)
	}
	if !strings.Contains(result, "342元") {
		t.Errorf("expected result to contain assistant message content, got: %s", result)
	}
}
