package agent

import (
	"encoding/json"
	"testing"

	"xhan-helper/internal/llm"
)

// done 载荷是手拼 JSON：错误文案里的引号/换行不转义会让前端 JSON.parse 失败，
// 整个 done 事件被丢掉（真实 message_id / 会话 id / 灵阅卡片全没了）。
func TestEmitDoneEscapesError(t *testing.T) {
	ch := make(chan *llm.StreamEvent, 1)
	o := &Orchestrator{}
	errStr := "he said \"x\"\nnext line"
	o.emitDone(ch, "conv1", "msg1", "user1", errStr, nil, nil, 0, 0)

	evt := <-ch
	if evt.Type != "done" {
		t.Fatalf("event type = %q, want done", evt.Type)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(evt.Content), &payload); err != nil {
		t.Fatalf("done payload is not valid JSON: %v (%s)", err, evt.Content)
	}
	if payload["error"] != errStr {
		t.Fatalf("error = %q, want %q", payload["error"], errStr)
	}
	if payload["conversation_id"] != "conv1" || payload["message_id"] != "msg1" || payload["user_message_id"] != "user1" {
		t.Fatalf("ids lost: %v", payload)
	}
}
