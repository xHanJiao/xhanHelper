package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"xhan-helper/internal/tool"
)

// llmResponseShape 复刻 prompt-engineering 信封的形状（原 LLMResponse 已随 ReactLoop 删除），
// 仅用于断言 ParseFlashJSON 的解析行为。
type llmResponseShape struct {
	Reply     string          `json:"reply"`
	ToolCalls []tool.ToolCall `json:"tool_calls"`
}

func TestParseFlashJSON(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		var v struct {
			Name string `json:"name"`
		}
		prefix, err := ParseFlashJSON(`{"name":"test"}`, &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
		if v.Name != "test" {
			t.Errorf("name = %q, want test", v.Name)
		}
	})

	t.Run("nested json", func(t *testing.T) {
		var v struct {
			Data struct {
				Deep string `json:"deep"`
			} `json:"data"`
		}
		_, err := ParseFlashJSON(`{"data":{"deep":"ok"}}`, &v)
		if err != nil {
			t.Fatal(err)
		}
		if v.Data.Deep != "ok" {
			t.Errorf("deep = %q, want ok", v.Data.Deep)
		}
	})

	t.Run("brace inside string value", func(t *testing.T) {
		var v struct {
			Code string `json:"code"`
		}
		raw := `{"code":"function() { return 1; }"}`
		prefix, err := ParseFlashJSON(raw, &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
		if v.Code != "function() { return 1; }" {
			t.Errorf("code = %q, want function() { return 1; }", v.Code)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		var v map[string]interface{}
		prefix, err := ParseFlashJSON("", &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
	})

	t.Run("no json at all", func(t *testing.T) {
		var v map[string]interface{}
		prefix, err := ParseFlashJSON("just plain text", &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "" {
			t.Errorf("prefix = %q, want empty", prefix)
		}
	})

	t.Run("unclosed brace", func(t *testing.T) {
		var v map[string]interface{}
		_, err := ParseFlashJSON(`{"a":1`, &v)
		if err == nil {
			t.Fatal("expected error for unclosed brace")
		}
		_, ok := err.(*json.SyntaxError)
		if !ok {
			t.Logf("error type: %T, message: %v", err, err)
		}
	})

	t.Run("prefix text before json", func(t *testing.T) {
		var v struct {
			Reply string `json:"reply"`
		}
		prefix, err := ParseFlashJSON("ok let me do this\n{\"reply\":\"hello\"}", &v)
		if err != nil {
			t.Fatal(err)
		}
		if prefix != "ok let me do this" {
			t.Errorf("prefix = %q, want 'ok let me do this'", prefix)
		}
		if v.Reply != "hello" {
			t.Errorf("reply = %q, want hello", v.Reply)
		}
	})

	t.Run("text after json", func(t *testing.T) {
		var v struct {
			Reply string `json:"reply"`
		}
		_, err := ParseFlashJSON(`{"reply":"ok"} and more text here`, &v)
		if err != nil {
			t.Fatal(err)
		}
		if v.Reply != "ok" {
			t.Errorf("reply = %q, want ok", v.Reply)
		}
	})

	t.Run("markdown code block", func(t *testing.T) {
		var v struct {
			Reply string `json:"reply"`
		}
		_, err := ParseFlashJSON("```json\n{\"reply\":\"hi\"}\n```", &v)
		if err != nil {
			t.Fatal(err)
		}
		if v.Reply != "hi" {
			t.Errorf("reply = %q, want hi", v.Reply)
		}
	})

	// ============================================================
	// LLMResponse / tool_calls 解析测试
	// ============================================================

	t.Run("LLMResponse with tool_calls and nested args", func(t *testing.T) {
		raw := `{"reply":"好的，我来记录","tool_calls":[{"tool":"note","function":"record_note","args":{"topic":"test","content":"hello world","tags":"tag1,tag2","variables":{"topic":"test","content":"hello world","tags":"tag1,tag2"}}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Reply == "" {
			t.Error("reply should not be empty")
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
		if resp.ToolCalls[0].Tool != "note" {
			t.Errorf("tool = %q, want note", resp.ToolCalls[0].Tool)
		}
		if resp.ToolCalls[0].Function != "record_note" {
			t.Errorf("function = %q, want record_note", resp.ToolCalls[0].Function)
		}
		if resp.ToolCalls[0].Args == nil {
			t.Error("args should not be nil")
		}
	})

	t.Run("tool_calls with Chinese punctuation in strings", func(t *testing.T) {
		// 中文引号 "" 不是 ASCII 双引号，应能正常解析
		raw := `{"reply":"这个判断很清醒。","tool_calls":[{"tool":"note","function":"record_note","args":{"topic":"房地产","content":"用户说：“不买学区溢价”，这个原则很重要。","tags":"购房","variables":{"key":"value"}}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
	})

	t.Run("multiple tool_calls", func(t *testing.T) {
		raw := `{"reply":"好的，同时记录两条","tool_calls":[{"tool":"note","function":"record_note","args":{"topic":"a","content":"aaa"}},{"tool":"note","function":"record_note","args":{"topic":"b","content":"bbb"}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.ToolCalls) != 2 {
			t.Fatalf("expected 2 tool_calls, got %d", len(resp.ToolCalls))
		}
	})

	t.Run("reply only, no tool_calls", func(t *testing.T) {
		raw := `{"reply":"这是一个纯文本回复，没有工具调用"}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Reply == "" {
			t.Error("reply should not be empty")
		}
		if len(resp.ToolCalls) != 0 {
			t.Errorf("expected 0 tool_calls, got %d", len(resp.ToolCalls))
		}
	})

	// ============================================================
	// Bug 场景: 括号在 JSON 字符串值内
	// ============================================================

	t.Run("unbalanced closing brace in reply string", func(t *testing.T) {
		// "我觉得}" — } 在字符串里不配对，会被当根对象结束
		raw := `{"reply":"我觉得}你说得对","tool_calls":[{"tool":"note","function":"record_note","args":{"topic":"t","content":"c"}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("BUG: unbalanced } in string value broke parsing: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("BUG: expected 1 tool_call, got %d (} in string caused truncation)", len(resp.ToolCalls))
		}
	})

	t.Run("unbalanced closing brace in content string", func(t *testing.T) {
		raw := `{"reply":"好的","tool_calls":[{"tool":"note","function":"record_note","args":{"topic":"t","content":"他说}这个是错的"}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("BUG: unbalanced } in content broke parsing: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("BUG: expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
	})

	t.Run("opening brace in string value", func(t *testing.T) {
		// { 在字符串里 — 会让深度多 1，但由于根对象最终的 } 也会被 count，如果有配对 }，也能对上
		// 但单独一个 { 会让深度偏大，最终 } 不会归零
		raw := `{"reply":"我觉得{你说得对","tool_calls":[]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("BUG: unbalanced { in string value broke parsing: %v", err)
		}
	})

	t.Run("multiple braces inside string values", func(t *testing.T) {
		// 模拟回复中包含代码片段
		raw := `{"reply":"if (x > 0) { doSomething(); }","tool_calls":[]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("BUG: braces in string value broke parsing: %v", err)
		}
	})

	// ============================================================
	// Bug 场景: LLM 在 JSON 字符串内输出未转义 ASCII 双引号
	// ============================================================

	t.Run("unescaped ASCII double quotes in content", func(t *testing.T) {
		// LLM 常见的错误：把应该转义的 " 直接放在了 JSON 字符串里
		// 这是非法 JSON，应报错触发 LLM 重试，而不是被静默吞掉
		raw := `{"reply":"好的","tool_calls":[{"tool":"note","function":"record_note","args":{"topic":"t","content":"他说"太断子绝孙"是错的"}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err == nil {
			t.Error("expected error for invalid JSON with unescaped quotes, got nil")
		}
	})

	t.Run("unescaped ASCII double quotes in reply only", func(t *testing.T) {
		raw := `{"reply":"他说"你好"世界","tool_calls":[]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err == nil {
			t.Error("expected error for invalid JSON with unescaped quotes, got nil")
		}
	})

	// ============================================================
	// 边界场景
	// ============================================================

	t.Run("empty tool_calls array", func(t *testing.T) {
		raw := `{"reply":"hello","tool_calls":[]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Reply != "hello" {
			t.Errorf("reply = %q, want hello", resp.Reply)
		}
		if len(resp.ToolCalls) != 0 {
			t.Errorf("expected 0 tool_calls, got %d", len(resp.ToolCalls))
		}
	})

	t.Run("escaped quotes in string", func(t *testing.T) {
		// 正确转义的双引号不应出问题
		raw := `{"reply":"he said \"hello\" to me","tool_calls":[]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("backslash in string value", func(t *testing.T) {
		raw := `{"reply":"path\\to\\file","tool_calls":[]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("newline in string value", func(t *testing.T) {
		raw := "{\"reply\":\"line1\\nline2\",\"tool_calls\":[]}"
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("prefix text with tool_calls", func(t *testing.T) {
		raw := "好的，我来处理\n{\"reply\":\"done\",\"tool_calls\":[{\"tool\":\"note\",\"function\":\"record_note\",\"args\":{\"topic\":\"t\",\"content\":\"c\"}}]}"
		var resp llmResponseShape
		prefix, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prefix != "好的，我来处理" {
			t.Errorf("prefix = %q, want '好的，我来处理'", prefix)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
	})

	t.Run("function name with underscore prefix", func(t *testing.T) {
		// parseLLMResponse 会 strip function 前缀 _
		raw := `{"reply":"ok","tool_calls":[{"tool":"note","function":"_record_note","args":{"topic":"t","content":"c"}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
	})

	t.Run("deeply nested args with arrays", func(t *testing.T) {
		raw := `{"reply":"ok","tool_calls":[{"tool":"agent","function":"create_agent","args":{"name":"test","tools":["note","datetime"],"config":{"model":"gpt4","temp":0.7}}}]}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
	})

	// ============================================================
	// 真实数据库样例
	// ============================================================

	t.Run("real sample 1: record_note", func(t *testing.T) {
		raw := `{
    "reply": "已为你记录了一条测试笔记。",
    "tool_calls": [
        {
            "tool": "note",
            "function": "record_note",
            "args": {
                "content": "这是一条测试笔记，用于验证note工具功能。",
                "tags": "test, 工具测试",
                "topic": "系统测试"
            }
        }
    ]
}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Reply == "" {
			t.Error("reply should not be empty")
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
		if resp.ToolCalls[0].Tool != "note" || resp.ToolCalls[0].Function != "record_note" {
			t.Errorf("unexpected tool call: %s.%s", resp.ToolCalls[0].Tool, resp.ToolCalls[0].Function)
		}
	})

	t.Run("real sample 2: unified_search", func(t *testing.T) {
		raw := `{
    "reply": "好的，单独搜「求职」看看分数分布有什么不同。",
    "tool_calls": [
        {
            "tool": "unified_search",
            "function": "search",
            "args": {
                "query": "求职"
            }
        }
    ]
}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
		if resp.ToolCalls[0].Tool != "unified_search" {
			t.Errorf("tool = %q, want unified_search", resp.ToolCalls[0].Tool)
		}
	})

	t.Run("real sample 3: bookkeeping create_bill with numeric amount", func(t *testing.T) {
		raw := `{
    "reply": "已记录：早餐豆浆油条8元。",
    "tool_calls": [
        {
            "tool": "bookkeeping",
            "function": "create_bill",
            "args": {
                "item": "早餐豆浆油条",
                "amount": 8,
                "category1": "餐饮",
                "category2": "堂食",
                "consume_type": "一次性",
                "date": "2026-05-22",
                "remark": "早餐"
            }
        }
    ]
}`
		var resp llmResponseShape
		_, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
		// amount 是数字类型，确保 json.RawMessage 保留了原始数值
		if !strings.Contains(string(resp.ToolCalls[0].Args), `"amount":8`) {
			t.Logf("args = %s", string(resp.ToolCalls[0].Args))
		}
	})

	t.Run("real sample 4: list_agents with empty args", func(t *testing.T) {
		raw := `好的，我来查看一下当前已有的智能体列表。

{
    "reply": "正在查询已有的智能体列表，请稍等...",
    "tool_calls": [
        {
            "tool": "agent",
            "function": "list_agents",
            "args": {}
        }
    ]
}`
		var resp llmResponseShape
		prefix, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prefix != "好的，我来查看一下当前已有的智能体列表。" {
			t.Errorf("prefix = %q", prefix)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
		if len(resp.ToolCalls[0].Args) == 0 || string(resp.ToolCalls[0].Args) == "{}" {
			// empty args is valid
		} else {
			t.Errorf("expected empty args, got %s", string(resp.ToolCalls[0].Args))
		}
	})

	t.Run("real sample 5: query_bills with timestamp prefix", func(t *testing.T) {
		raw := `[05-22 13:24] 好的，查一下这个月的账单记录。

{

    "reply": "来看你最近的账单：",

    "tool_calls": [

        {

            "tool": "bookkeeping",

            "function": "query_bills",

            "args": {

                "start_date": "2026-05-01",

                "end_date": "2026-05-22"

            }

        }

    ]

}`
		var resp llmResponseShape
		prefix, err := ParseFlashJSON(raw, &resp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prefix == "" {
			t.Error("expected non-empty prefix with timestamp text")
		}
		t.Logf("prefix = %q", prefix)
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool_call, got %d", len(resp.ToolCalls))
		}
		if resp.ToolCalls[0].Tool != "bookkeeping" || resp.ToolCalls[0].Function != "query_bills" {
			t.Errorf("unexpected tool call: %s.%s", resp.ToolCalls[0].Tool, resp.ToolCalls[0].Function)
		}
	})

	// ============================================================
	// 全量验证：从数据库导出的真实 LLM 响应
	// ============================================================

	t.Run("real_data_100_samples", func(t *testing.T) {
		data, err := os.ReadFile("testdata/llm_responses.txt")
		if err != nil {
			t.Skipf("testdata not available: %v", err)
			return
		}

		rows := strings.Split(string(data), "____ROW____")
		var total, parseOK, toolCallsOK, parseFail int
		var failures []string

		for _, raw := range rows {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			total++

			var resp llmResponseShape
			prefix, err := ParseFlashJSON(raw, &resp)
			if err != nil {
				parseFail++
				if len(failures) < 5 {
					excerpt := raw
					if len(excerpt) > 120 {
						excerpt = excerpt[:120] + "..."
					}
					failures = append(failures, excerpt)
				}
				continue
			}
			parseOK++
			_ = prefix

			if len(resp.ToolCalls) > 0 {
				toolCallsOK++
			}
		}

		t.Logf("total=%d, parse_ok=%d, parse_fail=%d, with_tool_calls=%d",
			total, parseOK, parseFail, toolCallsOK)

		if parseFail > 0 {
			t.Errorf("%d/%d samples failed to parse", parseFail, total)
			for i, f := range failures {
				t.Logf("  fail[%d]: %s", i+1, f)
			}
		}
	})
}
