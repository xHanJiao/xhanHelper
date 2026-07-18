package agent

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestClientToolBridge_NormalFlow(t *testing.T) {
	b := NewClientToolBridge()
	ch := b.Register("call-1", "user-a")

	err := b.OnResult("call-1", "user-a", ClientToolResult{Result: json.RawMessage(`{"ok":1}`)})
	if err != nil {
		t.Fatalf("OnResult error: %v", err)
	}

	select {
	case r := <-ch:
		if string(r.Result) != `{"ok":1}` {
			t.Errorf("unexpected result: %s", r.Result)
		}
	default:
		t.Fatal("expected result in channel")
	}
}

func TestClientToolBridge_UnknownCallID(t *testing.T) {
	b := NewClientToolBridge()
	if err := b.OnResult("no-such-call", "user-a", ClientToolResult{}); err == nil {
		t.Fatal("expected error for unknown call_id")
	}
}

func TestClientToolBridge_UserMismatch(t *testing.T) {
	b := NewClientToolBridge()
	ch := b.Register("call-1", "user-a")

	if err := b.OnResult("call-1", "user-b", ClientToolResult{}); err == nil {
		t.Fatal("expected error for user mismatch")
	}

	// mismatch 不应消费 pending，真正的用户仍可回传
	if err := b.OnResult("call-1", "user-a", ClientToolResult{Error: "boom"}); err != nil {
		t.Fatalf("legit OnResult after mismatch failed: %v", err)
	}
	r := <-ch
	if r.Error != "boom" {
		t.Errorf("unexpected error field: %s", r.Error)
	}
}

func TestClientToolBridge_DuplicateResult(t *testing.T) {
	b := NewClientToolBridge()
	b.Register("call-1", "user-a")

	if err := b.OnResult("call-1", "user-a", ClientToolResult{}); err != nil {
		t.Fatalf("first OnResult error: %v", err)
	}
	if err := b.OnResult("call-1", "user-a", ClientToolResult{}); err == nil {
		t.Fatal("expected error for duplicate result")
	}
}

func TestClientToolBridge_UnregisterThenResult(t *testing.T) {
	b := NewClientToolBridge()
	b.Register("call-1", "user-a")
	b.Unregister("call-1")
	b.Unregister("call-1") // 幂等

	if err := b.OnResult("call-1", "user-a", ClientToolResult{}); err == nil {
		t.Fatal("expected error after unregister")
	}
}

func TestClientToolBridge_Concurrent(t *testing.T) {
	b := NewClientToolBridge()
	const n = 100

	chs := make([]<-chan ClientToolResult, n)
	for i := 0; i < n; i++ {
		chs[i] = b.Register(callID(i), "user-a")
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := b.OnResult(callID(i), "user-a", ClientToolResult{Result: json.RawMessage(`1`)}); err != nil {
				t.Errorf("OnResult(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		select {
		case <-chs[i]:
		default:
			t.Errorf("missing result for call %d", i)
		}
	}
}

func callID(i int) string {
	return string(rune('a'+i%26)) + "-" + string(rune('0'+i/26))
}
