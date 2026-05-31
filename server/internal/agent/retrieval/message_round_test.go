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

	t.Run("keeps only last assistant", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m3, m4}
		got := FilterMessageRound(msgs, "m1")
		// 只保留 user + 最后一个 assistant（m4），中间 assistant（m2）丢弃
		if len(got) != 2 {
			t.Errorf("expected 2 messages (user + last assistant), got %d", len(got))
		}
		if got[1].ID != "m4" {
			t.Errorf("expected last assistant m4, got %s", got[1].ID)
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
			t.Errorf("expected 2 messages, got %d", len(got))
		}
		if got[1].ID != "m2" {
			t.Errorf("expected assistant msg m2, got %s", got[1].ID)
		}
	})

	t.Run("full round chain with multiple assistants", func(t *testing.T) {
		msgs := []model.Message{m1, m2, m3, m4, m5}
		got := FilterMessageRound(msgs, "m1")
		// 只保留 m1(user) + m4(最后一个 assistant)，跳过 m2 和 m5(下一轮 user)
		if len(got) != 2 {
			t.Errorf("expected 2 messages (user + last assistant), got %d", len(got))
		}
		if got[0].ID != "m1" || got[1].ID != "m4" {
			t.Errorf("expected [m1, m4], got %v", got)
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

	// —— ReAct 多轮：中间 assistant 是 tool call JSON，只保留最后一条有实际回复的 ——
	t.Run("react multi-round: keeps only last meaningful assistant", func(t *testing.T) {
		reactMid1 := model.Message{ID: "r2", Role: "assistant",
			Content:   `{"reply":"","tool_calls":[{"tool":"note","function":"search","args":{"limit":3,"query":"2026-05-27"}}]}`,
			CreatedAt: now.Add(time.Second)}
		toolResult := model.Message{ID: "r3", Role: "tool", Content: `{"results":[]}`, CreatedAt: now.Add(2 * time.Second), IsInternal: true}
		reactMid2 := model.Message{ID: "r4", Role: "assistant",
			Content:   `{"reply":"","tool_calls":[{"tool":"memory_search","function":"search","args":{"limit":3,"query":"2026-05-27"}}]}`,
			CreatedAt: now.Add(3 * time.Second)}
		toolResult2 := model.Message{ID: "r5", Role: "tool", Content: `{"results":[]}`, CreatedAt: now.Add(4 * time.Second), IsInternal: true}
		finalReply := model.Message{ID: "r6", Role: "assistant",
			Content:   "你2026-05-27过得挺充实啊——vibe coding一整天，晚上搞了两瓶福佳白啤😄",
			CreatedAt: now.Add(5 * time.Second)}

		msgs := []model.Message{m1, reactMid1, toolResult, reactMid2, toolResult2, finalReply}
		got := FilterMessageRound(msgs, "m1")
		if len(got) != 2 {
			t.Fatalf("expected 2 messages (user + last assistant), got %d: %v", len(got), got)
		}
		if got[0].Role != "user" || got[1].Role != "assistant" {
			t.Errorf("expected [user, assistant], got roles: %s, %s", got[0].Role, got[1].Role)
		}
		if got[1].Content != finalReply.Content {
			t.Errorf("expected final reply, got: %s", got[1].Content)
		}
	})

	// —— verification 场景：内部草稿 assistant 不应该是"最后一个"
	t.Run("verify draft: skips IsInternal assistant", func(t *testing.T) {
		firstReply := model.Message{ID: "v2", Role: "assistant", Content: "账单：福佳白啤22元", CreatedAt: now.Add(time.Second)}
		verifyDraft := model.Message{ID: "v3", Role: "assistant", Content: "账单：福佳白啤22元（草稿）", CreatedAt: now.Add(3 * time.Second), IsInternal: true}
		verifyFeedback := model.Message{ID: "v4", Role: "system", Content: "检验不通过：缺少金额明细", CreatedAt: now.Add(4 * time.Second), IsInternal: true}
		finalReply := model.Message{ID: "v5", Role: "assistant", Content: "账单：福佳白啤22元，5月1日现金支付", CreatedAt: now.Add(5 * time.Second)}
		verifyPass := model.Message{ID: "v6", Role: "system", Content: "检验通过", CreatedAt: now.Add(6 * time.Second), IsInternal: true}
		confirmed := model.Message{ID: "v7", Role: "assistant", Content: "账单：福佳白啤22元，5月1日现金支付", CreatedAt: now.Add(7 * time.Second)}

		msgs := []model.Message{m1, firstReply, verifyDraft, verifyFeedback, finalReply, verifyPass, confirmed}
		got := FilterMessageRound(msgs, "m1")
		if len(got) != 2 {
			t.Fatalf("expected 2 messages (user + last non-internal assistant), got %d", len(got))
		}
		if got[1].IsInternal {
			t.Errorf("last assistant must not be IsInternal, got %+v", got[1])
		}
		// 应该是 v7（confirmed），不是 v3（verifyDraft）
		if got[1].ID != "v7" {
			t.Errorf("expected confirmed v7, got %s", got[1].ID)
		}
	})

	// —— 没有 assistant 的轮：只有 user 消息
	t.Run("round with no assistant", func(t *testing.T) {
		got := FilterMessageRound([]model.Message{m1}, "m1")
		if len(got) != 1 {
			t.Errorf("expected 1 message (user only), got %d", len(got))
		}
		if got[0].Role != "user" {
			t.Errorf("expected user message, got %s", got[0].Role)
		}
	})
}

func TestFormatMessageRound(t *testing.T) {
	now := time.Now()
	msgs := []model.Message{
		{ID: "m1", ConversationID: "conv1", Role: "user", Content: "帮我查账单", CreatedAt: now},
		{ID: "m2", ConversationID: "conv1", Role: "assistant", Content: "你的账单如下：共计342元", CreatedAt: now.Add(time.Second)},
	}

	result := FormatMessageRoundWithTitle(msgs, "测试会话")
	if !strings.Contains(result, "测试会话") {
		t.Errorf("expected result to contain conversation title, got: %s", result)
	}
	if !strings.Contains(result, "用户:") {
		t.Errorf("expected result to contain user indicator, got: %s", result)
	}
	if !strings.Contains(result, "助手:") {
		t.Errorf("expected result to contain assistant indicator, got: %s", result)
	}
	if !strings.Contains(result, "帮我查账单") {
		t.Errorf("expected result to contain user message content, got: %s", result)
	}
	if !strings.Contains(result, "342元") {
		t.Errorf("expected result to contain assistant message content, got: %s", result)
	}
}

func TestFormatMessageRound_WithScore(t *testing.T) {
	now := time.Now()
	msgs := []model.Message{
		{ID: "m1", Role: "user", Content: "帮我查账单", CreatedAt: now},
		{ID: "m2", Role: "assistant", Content: "你的账单如下：共计342元", CreatedAt: now.Add(time.Second)},
	}

	result := FormatMessageRound(msgs, 0.87)
	if !strings.Contains(result, "相关度 0.87") {
		t.Errorf("expected score header, got: %s", result)
	}
	if !strings.Contains(result, "**用户**") {
		t.Errorf("expected bold user role, got: %s", result)
	}
	if !strings.Contains(result, "**助手**") {
		t.Errorf("expected bold assistant role, got: %s", result)
	}
	if !strings.Contains(result, "帮我查账单") {
		t.Errorf("expected user content, got: %s", result)
	}
	if !strings.Contains(result, "342元") {
		t.Errorf("expected assistant content, got: %s", result)
	}
}
