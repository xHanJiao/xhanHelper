package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeRecordRunner struct{ calls int }

func (f *fakeRecordRunner) RecordInline(ctx context.Context, userID, convID, topic, summary string, facts []string) ([]byte, error) {
	f.calls++
	return []byte(`{"action":"add","id":"m1"}`), nil
}

func execRecordMemory(t *testing.T, tr *RecordMemoryTool, convID string) *Result {
	t.Helper()
	return tr.Execute(
		&ExecuteContext{Ctx: context.Background(), UserID: "u1", ConvID: convID},
		"record_memory",
		json.RawMessage(`{"topic":"测试","summary":"一句话"}`),
	)
}

// 无痕会话：拒写，且决策循环根本不该被调到（少了 LLM 调用与落库）。
func TestRecordMemory_EphemeralConv_DoesNotWrite(t *testing.T) {
	runner := &fakeRecordRunner{}
	tr := NewRecordMemoryTool(runner, func(context.Context, string) (bool, error) { return true, nil })

	res := execRecordMemory(t, tr, "conv-ephemeral")
	if res.Success {
		t.Fatalf("expected failure in ephemeral conv, got success: %+v", res)
	}
	if runner.calls != 0 {
		t.Errorf("runner must not be called in ephemeral conv, got %d calls", runner.calls)
	}
}

func TestRecordMemory_NormalConv_Writes(t *testing.T) {
	runner := &fakeRecordRunner{}
	tr := NewRecordMemoryTool(runner, func(context.Context, string) (bool, error) { return false, nil })

	res := execRecordMemory(t, tr, "conv-normal")
	if !res.Success {
		t.Fatalf("expected success in normal conv, got: %+v", res)
	}
	if runner.calls != 1 {
		t.Errorf("expected 1 runner call, got %d", runner.calls)
	}
}

// 查不到会话类型就拒写（隐私侧 fail-closed），不能因为一次 DB 抖动把记忆写进可能是无痕的会话。
func TestRecordMemory_CheckerError_FailsClosed(t *testing.T) {
	runner := &fakeRecordRunner{}
	tr := NewRecordMemoryTool(runner, func(context.Context, string) (bool, error) {
		return false, errors.New("db down")
	})

	res := execRecordMemory(t, tr, "conv-unknown")
	if res.Success {
		t.Fatalf("expected failure when checker errors, got success: %+v", res)
	}
	if runner.calls != 0 {
		t.Errorf("runner must not be called when checker errors, got %d calls", runner.calls)
	}
}

// 无会话上下文（replay / eval）不查：按非无痕放行。
func TestRecordMemory_EmptyConvID_SkipsCheck(t *testing.T) {
	runner := &fakeRecordRunner{}
	tr := NewRecordMemoryTool(runner, func(context.Context, string) (bool, error) {
		t.Fatal("checker must not be called when convID is empty")
		return true, nil
	})

	if res := execRecordMemory(t, tr, ""); !res.Success {
		t.Fatalf("expected success without convID, got: %+v", res)
	}
	if runner.calls != 1 {
		t.Errorf("expected 1 runner call, got %d", runner.calls)
	}
}

func TestNewRecordMemoryTool_NilCheckerPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil ephemeralCheck (wiring bug)")
		}
	}()
	NewRecordMemoryTool(&fakeRecordRunner{}, nil)
}
