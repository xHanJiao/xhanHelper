package controller

import (
	"testing"
	"time"

	"xhan-helper/internal/model"
)

func TestCutRound(t *testing.T) {
	now := time.Now()
	mkMsg := func(id, role, convID string, t time.Time) model.Message {
		return model.Message{ID: id, Role: role, ConversationID: convID, CreatedAt: t}
	}

	t.Run("normal round: user -> assistant -> tool -> assistant -> next user", func(t *testing.T) {
		msgs := []model.Message{
			mkMsg("u1", "user", "c1", now),
			mkMsg("a1", "assistant", "c1", now.Add(1*time.Second)),
			mkMsg("t1", "tool", "c1", now.Add(2*time.Second)),
			mkMsg("a2", "assistant", "c1", now.Add(3*time.Second)),
			mkMsg("u2", "user", "c1", now.Add(4*time.Second)),
		}
		got := cutRound(msgs, "u1")
		if len(got) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(got))
		}
		if got[len(got)-1].ID != "a2" {
			t.Errorf("last should be a2, got %s", got[len(got)-1].ID)
		}
	})

	t.Run("single user message with no follow-up", func(t *testing.T) {
		msgs := []model.Message{
			mkMsg("u1", "user", "c1", now),
		}
		got := cutRound(msgs, "u1")
		if len(got) != 1 {
			t.Fatalf("expected 1 message, got %d", len(got))
		}
	})

	t.Run("anchor not found returns empty", func(t *testing.T) {
		msgs := []model.Message{
			mkMsg("other", "user", "c1", now),
		}
		got := cutRound(msgs, "u1")
		if len(got) != 0 {
			t.Fatalf("expected 0 messages, got %d", len(got))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := cutRound(nil, "u1")
		if len(got) != 0 {
			t.Fatalf("expected 0 messages, got %d", len(got))
		}
	})

	t.Run("includes IsInternal tool messages", func(t *testing.T) {
		toolMsg := mkMsg("t1", "tool", "c1", now.Add(1*time.Second))
		toolMsg.IsInternal = true
		msgs := []model.Message{
			mkMsg("u1", "user", "c1", now),
			mkMsg("a1", "assistant", "c1", now.Add(500*time.Millisecond)),
			toolMsg,
			mkMsg("u2", "user", "c1", now.Add(2*time.Second)),
		}
		got := cutRound(msgs, "u1")
		if len(got) != 3 {
			t.Fatalf("expected 3 messages (including tool), got %d", len(got))
		}
		if got[2].ID != "t1" {
			t.Errorf("third should be t1 (tool), got %s", got[2].ID)
		}
	})
}

func TestCleanForDebug(t *testing.T) {
	reqJSON := "big json"
	respJSON := "big json"
	extra := "trace"
	msg := &model.Message{
		ID:           "m1",
		Content:      "hello",
		RequestJSON:  &reqJSON,
		ResponseJSON: &respJSON,
		Extra:        &extra,
	}
	cleaned := cleanForDebug(msg)
	if cleaned.ID != "m1" {
		t.Errorf("id should be preserved")
	}
	if cleaned.Content != "hello" {
		t.Errorf("content should be preserved")
	}
	if cleaned.RequestJSON != nil {
		t.Errorf("request_json should be nil")
	}
	if cleaned.ResponseJSON != nil {
		t.Errorf("response_json should be nil")
	}
	if cleaned.Extra != nil {
		t.Errorf("extra should be nil")
	}
	// original should not be mutated
	if msg.RequestJSON == nil {
		t.Errorf("original request_json should not be mutated")
	}
}
