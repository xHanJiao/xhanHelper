package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

func TestMiniReAct_DirectFinalContent(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "请提取画像")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalContent == "" {
		t.Fatal("expected FinalContent")
	}
	if !strings.Contains(result.FinalContent, "籍贯") {
		t.Errorf("unexpected content: %s", result.FinalContent)
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if result.TerminatedEarly {
		t.Error("should not terminate early")
	}
}

func TestMiniReAct_ToolCallThenFinal(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			Content: "",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"用户基本信息"}`}},
			},
		},
		{Content: `{"籍贯":"河南焦作"}`},
	}}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         registryWithUnifiedSearch().GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  registryWithUnifiedSearch(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "请提取画像")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
	if result.ToolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", result.ToolCalls)
	}
	if result.FinalContent != `{"籍贯":"河南焦作"}` {
		t.Errorf("unexpected final: %s", result.FinalContent)
	}
	// 工具结果应以 role=tool 回传
	if len(llmClient.reqs) < 2 {
		t.Fatalf("expected 2 requests, got %d", len(llmClient.reqs))
	}
	second := llmClient.reqs[1]
	found := false
	for _, msg := range second.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call-1" && strings.Contains(msg.Content, "result") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected role=tool message with ToolCallID=call-1, got messages: %+v", second.Messages)
	}
}

func TestMiniReAct_MaxIterationsTerminates(t *testing.T) {
	// 每轮都返回 tool_call，永不产出最终内容
	llmClient := &syncMockLLM{responses: make([]llm.Message, 3)}
	for i := range llmClient.responses {
		llmClient.responses[i] = llm.Message{ToolCalls: []llm.ToolCall{
			{ID: "call", Function: llm.ToolCallFunction{Name: "unified_search__semantic_search", Arguments: `{"query":"x"}`}},
		}}
	}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         registryWithUnifiedSearch().GetLLMToolsFiltered([]string{"unified_search"}),
		ToolRegistry:  registryWithUnifiedSearch(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 3,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.TerminatedEarly {
		t.Error("should terminate early at max iterations")
	}
	if result.FinalContent != "" {
		t.Errorf("no final content expected, got: %s", result.FinalContent)
	}
	if result.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", result.Iterations)
	}
}

func TestMiniReAct_LLMError(t *testing.T) {
	failClient := &errorLLMClient{err: errors.New("boom")}
	m := NewMiniReAct(&MiniReActConfig{
		Client:        failClient,
		ToolRegistry:  noopRegistry(),
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	_, err := m.Run(context.Background(), "任务")
	if err == nil {
		t.Fatal("expected error from LLM")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected boom in error, got: %v", err)
	}
}

func TestMiniReAct_PanicInToolRecovered(t *testing.T) {
	llmClient := &syncMockLLM{responses: []llm.Message{
		{
			Content: "",
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.ToolCallFunction{Name: "panic_tool__boom", Arguments: `{}`}},
			},
		},
		{Content: `{}`},
	}}
	r := tool.NewRegistry()
	r.Register(&panicTool{})
	m := NewMiniReAct(&MiniReActConfig{
		Client:        llmClient,
		Tools:         r.GetLLMToolsFiltered([]string{"panic_tool"}),
		ToolRegistry:  r,
		SystemPrompt:  "sys",
		UserID:        "user-1",
		MaxIterations: 5,
		Caller:        "test",
	})
	result, err := m.Run(context.Background(), "任务")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalContent != `{}` {
		t.Errorf("expected final after panic recovery, got: %s", result.FinalContent)
	}
}

// errorLLMClient 始终返回错误的 LLM client。
type errorLLMClient struct {
	err error
}

func (e *errorLLMClient) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	return llm.Message{}, e.err
}
func (e *errorLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return nil, e.err
}
func (e *errorLLMClient) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return llm.Message{}, e.err
}
func (e *errorLLMClient) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return base
}
func (e *errorLLMClient) ModelName() string { return "mock" }

// panicTool 工具执行时 panic，验证 MiniReAct 恢复逻辑。
type panicTool struct{}

func (t *panicTool) Name() string { return "panic_tool" }
func (t *panicTool) Description() string {
	return "panic tool"
}
func (t *panicTool) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{Name: "boom", Description: "boom", Parameters: map[string]interface{}{}}}
}
func (t *panicTool) ReadOnlyFuncs() map[string]bool { return nil }
func (t *panicTool) Execute(_ *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	panic("intentional panic")
}
