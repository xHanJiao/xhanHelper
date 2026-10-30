package agent

import (
	"testing"

	"xhan-helper/internal/model"
)

// 事故现场回归：同一会话并发两个 ReAct 循环交叉写库，产生
// assistant(tool_calls) → user → tool 的错位结构。修复前 DeepSeek 400
// "Messages with role 'tool' must be a response to a preceding message with 'tool_calls'"
// 导致会话报废；修复后孤儿 tool 消息降级为 user，请求可正常发送。
func TestBuildMessagesForFlash_OrphanToolAfterUser(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "看一下代码"},
		{Role: "assistant", Content: "看到关键线索了，读一下具体实现",
			ToolCallsJSON: strPtr(`[{"id":"call_00_aG6XEaYh","function":{"name":"fs__read","arguments":"{}"}},{"id":"call_01_N099sa9f","function":{"name":"fs__read","arguments":"{}"}}]`)},
		// 用户以为卡死重发的新消息（新循环先写入）
		{Role: "user", Content: "刚才卡死了，你再看一次吧"},
		// 旧循环超时后晚到的工具结果（created_at 晚于上面那条 user）
		{Role: "tool", Content: `{"success":false,"error":"客户端工具执行超时"}`, ToolCallID: strPtr("call_00_a")},
		{Role: "tool", Content: `{"success":false,"error":"客户端本地工具不可用"}`, ToolCallID: strPtr("call_01_N099")},
	}

	msgs := BuildMessagesForFlash(history, 2000, true, false)

	if len(msgs) != 5 {
		t.Fatalf("expected 5 msgs, got %d", len(msgs))
	}
	// 关键断言：不允许出现 role=tool（否则 DeepSeek 返回 400 拒绝请求）
	for i, m := range msgs {
		if m.Role == "tool" {
			t.Errorf("msgs[%d]: orphan tool message leaked to LLM as role=tool (tool_call_id=%s)", i, m.ToolCallID)
		}
	}
	// 两条迟到的工具结果应降级为 user（保留内容供 LLM 引用）
	if msgs[3].Role != "user" || msgs[3].ToolCallID != "" {
		t.Errorf("msgs[3]: expected degraded role=user without ToolCallID, got role=%s ToolCallID=%s", msgs[3].Role, msgs[3].ToolCallID)
	}
	if msgs[4].Role != "user" || msgs[4].ToolCallID != "" {
		t.Errorf("msgs[4]: expected degraded role=user without ToolCallID, got role=%s ToolCallID=%s", msgs[4].Role, msgs[4].ToolCallID)
	}
	// assistant 的孤儿 tool_calls 应被 sanitizeOrphanToolCalls 清空
	if len(msgs[1].ToolCalls) != 0 {
		t.Errorf("expected orphan tool_calls cleared on assistant, got %d", len(msgs[1].ToolCalls))
	}
}

// 孤儿 tool 消息（无任何前置 assistant tool_calls）也应降级为 user。
func TestBuildMessagesForFlash_OrphanToolNoPrecedingAssistant(t *testing.T) {
	history := []model.Message{
		{Role: "assistant", Content: "这条回复没有调用工具"},
		{Role: "tool", Content: `{"success":true}`, ToolCallID: strPtr("call_00_zzzz")},
	}

	msgs := BuildMessagesForFlash(history, 2000, true, false)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d", len(msgs))
	}
	if msgs[1].Role != "user" {
		t.Errorf("expected orphan tool degraded to user, got role=%s", msgs[1].Role)
	}
	if msgs[1].ToolCallID != "" {
		t.Errorf("expected ToolCallID cleared, got '%s'", msgs[1].ToolCallID)
	}
}

// 正常连续配对不受影响：assistant(tool_calls=[tc1,tc2]) → tool(tc1) → tool(tc2)
// 是合法的多结果连续序列，必须保持 role=tool。
func TestBuildMessagesForFlash_ConsecutiveToolsKept(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "查一下"},
		{Role: "assistant", Content: "",
			ToolCallsJSON: strPtr(`[{"id":"call_01","function":{"name":"fs__grep","arguments":"{}"}},{"id":"call_02","function":{"name":"fs__read","arguments":"{}"}}]`)},
		{Role: "tool", Content: `{"success":true,"data":"1"}`, ToolCallID: strPtr("call_01")},
		{Role: "tool", Content: `{"success":true,"data":"2"}`, ToolCallID: strPtr("call_02")},
		{Role: "assistant", Content: "查完了", ToolCallsJSON: strPtr(`[]`)},
	}

	msgs := BuildMessagesForFlash(history, 2000, true, false)

	if len(msgs) != 5 {
		t.Fatalf("expected 5 msgs, got %d", len(msgs))
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_01" {
		t.Errorf("msgs[2]: expected role=tool call_01, got role=%s id=%s", msgs[2].Role, msgs[2].ToolCallID)
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "call_02" {
		t.Errorf("msgs[3]: expected role=tool call_02, got role=%s id=%s", msgs[3].Role, msgs[3].ToolCallID)
	}
}

// 同一 assistant 的多个 tool 结果中，只有被 user 隔断的那个降级，
// 其余正常 tool 保持 role=tool。
func TestBuildMessagesForMessages_PartialOrphan(t *testing.T) {
	history := []model.Message{
		{Role: "user", Content: "分析"},
		{Role: "assistant", Content: "",
			ToolCallsJSON: strPtr(`[{"id":"call_01","function":{"name":"fs__read","arguments":"{}"}},{"id":"call_02","function":{"name":"fs__read","arguments":"{}"}}]`)},
		{Role: "tool", Content: `{"success":true,"data":"ok1"}`, ToolCallID: strPtr("call_01")},
		{Role: "user", Content: "等一下"}, // 隔断后续 tool
		{Role: "tool", Content: `{"success":true,"data":"ok2"}`, ToolCallID: strPtr("call_02")},
	}

	msgs := BuildMessagesForFlash(history, 2000, true, false)

	if len(msgs) != 5 {
		t.Fatalf("expected 5 msgs, got %d", len(msgs))
	}
	if msgs[2].Role != "tool" {
		t.Errorf("msgs[2]: expected role=tool (matched pair), got %s", msgs[2].Role)
	}
	if msgs[4].Role != "user" {
		t.Errorf("msgs[4]: expected orphan tool degraded to user, got role=%s", msgs[4].Role)
	}
	// assistant 中 call_01 有结果保留，call_02 无结果被剔除
	ids := make(map[string]bool)
	for _, tc := range msgs[1].ToolCalls {
		ids[tc.ID] = true
	}
	if !ids["call_01"] {
		t.Errorf("expected call_01 kept on assistant tool_calls")
	}
	if ids["call_02"] {
		t.Errorf("expected call_02 dropped from assistant tool_calls")
	}
}
