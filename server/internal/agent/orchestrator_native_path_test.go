package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// —— 主聊天核心路径：native FC only，文本一律原文，绝不做 Flash 信封解析 ——

func newCorePathOrch(repo msgCreator) *Orchestrator {
	return &Orchestrator{
		contextFormatter: &mockFormatter{},
		registry:         tool.NewRegistry(),
		msgRepo:          repo,
	}
}

// TestHandleOneRoundResponse_JSONContent_NotTruncated 是本次 bug 的定点回归：
// 正文含 JSON（命令 / JSON-RPC 示例）时，落库内容必须是完整原文，不能从第一个 '{' 起被截断。
func TestHandleOneRoundResponse_JSONContent_NotTruncated(t *testing.T) {
	repo := &recordingMsgRepo{}
	o := newCorePathOrch(repo)

	content := "在你的终端里跑：\n\n```\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}' | opencode acp\n```\n"
	msg := llm.Message{Role: "assistant", Content: content}
	state := &reactState{}

	done := o.handleOneRoundResponse(
		context.Background(), make(chan *llm.StreamEvent, 8), msg,
		&RunRequest{ConversationID: "c1", UserID: "u1"},
		"", (*ctxflow.RetrieveResult)(nil), state,
	)

	if !done {
		t.Fatal("纯文本回复应结束本轮（done=true）")
	}
	if len(repo.saved) != 1 {
		t.Fatalf("应保存 1 条 assistant 消息, got %d", len(repo.saved))
	}
	if repo.saved[0].Content != content {
		t.Errorf("落库内容被截断/改写\n got: %q\nwant: %q", repo.saved[0].Content, content)
	}
}

// TestHandleOneRoundResponse_EnvelopeTextWithToolCalls_NotExecuted：
// 即使文本形如 {"reply":...,"tool_calls":[...]} 的信封，native 主链路也不再解析执行，
// 只当普通文本原样落库（这正是旧兜底造成截断的根源，必须钉死）。
func TestHandleOneRoundResponse_EnvelopeTextWithToolCalls_NotExecuted(t *testing.T) {
	repo := &recordingMsgRepo{}
	o := newCorePathOrch(repo)

	content := `{"reply":"已记账","tool_calls":[{"tool":"bookkeeping","function":"create","args":{"amount":100}}]}`
	msg := llm.Message{Role: "assistant", Content: content}
	state := &reactState{}

	done := o.handleOneRoundResponse(
		context.Background(), make(chan *llm.StreamEvent, 8), msg,
		&RunRequest{ConversationID: "c1", UserID: "u1"},
		"", (*ctxflow.RetrieveResult)(nil), state,
	)

	if !done {
		t.Fatal("信封文本不应触发工具执行，应结束本轮")
	}
	if len(state.msgs) != 0 {
		t.Errorf("不应追加工具有关消息, got %d", len(state.msgs))
	}
	if len(state.allToolCalls) != 0 {
		t.Errorf("不应记录任何工具调用, got %d", len(state.allToolCalls))
	}
	if len(repo.saved) != 1 || repo.saved[0].Content != content {
		t.Errorf("信封文本应原样落库, got %+v", repo.saved)
	}
	if repo.saved[0].ToolCallsJSON != nil {
		t.Errorf("无 msg.ToolCalls 时不应从正文抽出 tool_calls_json, got %s", *repo.saved[0].ToolCallsJSON)
	}
}

// TestSaveAssistantMsg_ToolCallsJSON_FromNativeToolCalls：tool_calls_json 只来自 msg.ToolCalls。
func TestSaveAssistantMsg_ToolCallsJSON_FromNativeToolCalls(t *testing.T) {
	repo := &recordingMsgRepo{}
	o := newCorePathOrch(repo)

	tcs := []llm.ToolCall{{
		ID:       "call_1",
		Function: llm.ToolCallFunction{Name: "note__record_note", Arguments: `{"content":"x"}`},
	}}
	msg := llm.Message{Role: "assistant", Content: "好的", ToolCalls: tcs}

	o.saveAssistantMsg(context.Background(), "c1", "u1", msg, nil, false)

	if len(repo.saved) != 1 {
		t.Fatalf("应保存 1 条, got %d", len(repo.saved))
	}
	saved := repo.saved[0]
	if saved.Content != "好的" {
		t.Errorf("content 应保持原文, got %q", saved.Content)
	}
	if saved.ToolCallsJSON == nil {
		t.Fatal("应由 msg.ToolCalls 写出 tool_calls_json")
	}
	var got []llm.ToolCall
	if err := json.Unmarshal([]byte(*saved.ToolCallsJSON), &got); err != nil {
		t.Fatalf("tool_calls_json 应为合法 JSON: %v", err)
	}
	if len(got) != 1 || got[0].ID != "call_1" || got[0].Function.Name != "note__record_note" {
		t.Errorf("tool_calls_json 内容不符: %+v", got)
	}
}

// TestHandleOneRoundResponse_WithToolCalls_Continues：有原生 tool_calls 时保存并继续循环。
func TestHandleOneRoundResponse_WithToolCalls_Continues(t *testing.T) {
	repo := &recordingMsgRepo{}
	o := newCorePathOrch(repo)

	msg := llm.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []llm.ToolCall{{
			ID:       "call_1",
			Function: llm.ToolCallFunction{Name: "nonexistent__nope", Arguments: "{}"},
		}},
	}
	state := &reactState{}

	done := o.handleOneRoundResponse(
		context.Background(), make(chan *llm.StreamEvent, 8), msg,
		&RunRequest{ConversationID: "c1", UserID: "u1"},
		"", (*ctxflow.RetrieveResult)(nil), state,
	)

	if done {
		t.Fatal("有工具调用时应返回 false 继续 ReAct 循环")
	}
	if len(state.allToolCalls) != 1 {
		t.Errorf("应记录 1 个工具调用, got %d", len(state.allToolCalls))
	}
	// assistant（含 tool_calls）+ tool 结果各一条
	if len(repo.saved) != 2 {
		t.Errorf("应保存 assistant + tool 两条, got %d", len(repo.saved))
	}
	var savedToolCalls string
	for _, m := range repo.saved {
		if m.Role == "assistant" && m.ToolCallsJSON != nil {
			savedToolCalls = *m.ToolCallsJSON
		}
	}
	if !strings.Contains(savedToolCalls, "call_1") {
		t.Errorf("assistant 消息应带 tool_calls_json, got %q", savedToolCalls)
	}
}

// —— saveAssistantMsgWithFlags 的各存储分支 ——

type failingMsgRepo struct{}

func (failingMsgRepo) Create(context.Context, *model.Message) error           { return fmt.Errorf("db down") }
func (failingMsgRepo) SetArtifactsJSON(context.Context, string, string) error { return nil }

func TestSaveAssistantMsg_PersistsReasoningEncryptedUsageTrace(t *testing.T) {
	repo := &recordingMsgRepo{}
	o := newCorePathOrch(repo)

	msg := llm.Message{
		Role:             "assistant",
		Content:          "正文",
		ReasoningContent: "思考中",
		EncryptedContent: "enc-blob",
		Usage:            &llm.Usage{PromptTokens: 1, CompletionTokens: 2},
	}
	trace := &ctxflow.RetrieveTrace{Providers: []ctxflow.ProviderTrace{{Name: "memory", Type: "search"}}}

	o.saveAssistantMsg(context.Background(), "c1", "u1", msg, trace, false)

	if len(repo.saved) != 1 {
		t.Fatalf("应保存 1 条, got %d", len(repo.saved))
	}
	saved := repo.saved[0]
	if saved.Content != "正文" {
		t.Errorf("content = %q", saved.Content)
	}
	if saved.ReasoningContent == nil || *saved.ReasoningContent != "思考中" {
		t.Errorf("reasoning_content 未保存: %v", saved.ReasoningContent)
	}
	if saved.EncryptedContent == nil || *saved.EncryptedContent != "enc-blob" {
		t.Errorf("encrypted_content 未保存: %v", saved.EncryptedContent)
	}
	if saved.Extra == nil || !strings.Contains(*saved.Extra, "usage") {
		t.Errorf("extra 应含 usage: %v", saved.Extra)
	}
	if saved.Extra == nil || !strings.Contains(*saved.Extra, "memory") {
		t.Errorf("extra 应含 trace providers: %v", saved.Extra)
	}
}

func TestSaveAssistantMsg_CreateError_ReturnsEmpty(t *testing.T) {
	o := newCorePathOrch(failingMsgRepo{})
	mid := o.saveAssistantMsg(context.Background(), "c1", "u1", llm.Message{Role: "assistant", Content: "x"}, nil, false)
	if mid != "" {
		t.Errorf("落库失败应返回空 msgID, got %q", mid)
	}
}

func TestSaveAssistantMsg_DebugJSONSubmittedAfterCreate(t *testing.T) {
	repo := &recordingMsgRepo{}
	sink := &recordingDebugSink{}
	o := newCorePathOrch(repo)
	o.debugPersistJSON = true
	o.debugJSONSink = sink

	reqJSON := `{"messages":[]}`
	msg := llm.Message{Role: "assistant", Content: "x", RequestJSON: &reqJSON}
	mid := o.saveAssistantMsg(context.Background(), "c1", "u1", msg, nil, false)

	if mid == "" {
		t.Fatal("应返回 msgID")
	}
	if len(sink.submits) != 1 || sink.submits[0].id != mid {
		t.Errorf("落库成功后应提交一次 request_json, got %+v", sink.submits)
	}
}

// TestHandleOneRoundResponse_VerifyPath：验证开关打开且有工具调用历史时走 verify 分支。
func TestHandleOneRoundResponse_VerifyPath(t *testing.T) {
	repo := &recordingMsgRepo{}
	o := newCorePathOrch(repo)
	o.verifyEnabled = true
	o.verifyMaxRetries = 0 // 直接走「达到上限、接受草稿」分支，无需真实 LLM

	msg := llm.Message{Role: "assistant", Content: "草稿回复"}
	state := &reactState{allToolCalls: []tool.ToolCall{{Tool: "note", Function: "record_note"}}}

	done := o.handleOneRoundResponse(
		context.Background(), make(chan *llm.StreamEvent, 8), msg,
		&RunRequest{ConversationID: "c1", UserID: "u1"},
		"", (*ctxflow.RetrieveResult)(nil), state,
	)

	if !done {
		t.Fatal("verify 上限达到后应接受草稿并结束本轮")
	}
	if len(repo.saved) != 1 || repo.saved[0].Content != "草稿回复" {
		t.Errorf("草稿应原样落库, got %+v", repo.saved)
	}
}
