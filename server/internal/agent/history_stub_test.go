package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"xhan-helper/internal/model"
)

func htmlResultPage(html string) string {
	b, _ := json.Marshal(map[string]any{
		"success": true,
		"data": map[string]any{
			"artifact_id": "a1",
			"title":       "10.18 手账",
			"summary":     "一天的时间线手账",
			"html":        html,
		},
	})
	return string(b)
}

func TestStubToolResultForHistory(t *testing.T) {
	fullPage := htmlResultPage(strings.Repeat("<p>正文</p>", 500)) // ~5KB
	outline := `{"success":true,"data":{"artifact_id":"a1","blocks":[{"nid":"t1"}]}}`
	bigSearch := `{"success":true,"data":{"items":["` + strings.Repeat("结", 20000) + `"]}}`

	cases := []struct {
		name     string
		content  string
		tool     string
		maxChars int
		wantSame bool
		check    func(t *testing.T, out string)
	}{
		{
			name: "html_full_不看大小一律换掉", content: fullPage, tool: "html__get", maxChars: 20000,
			check: func(t *testing.T, out string) {
				var v map[string]any
				if err := json.Unmarshal([]byte(out), &v); err != nil {
					t.Fatalf("stub 应是合法 JSON: %v", err)
				}
				if v["html_omitted"] != true {
					t.Fatalf("应带 html_omitted: %s", out)
				}
				if _, ok := v["html"]; ok {
					t.Fatal("stub 不该带正文")
				}
				if v["artifact_id"] != "a1" || v["title"] != "10.18 手账" {
					t.Fatalf("应保留认出是哪一页的字段: %s", out)
				}
				if _, ok := v["page_bytes"].(float64); !ok {
					t.Fatalf("应带 page_bytes: %s", out)
				}
				if !strings.Contains(v["hint"].(string), "html.patch") || !strings.Contains(v["hint"].(string), "insert") {
					t.Fatalf("hint 要指向 outline + patch（含 insert/remove）: %s", out)
				}
			},
		},
		{
			name: "html_outline_原样", content: outline, tool: "html__get", maxChars: 20000, wantSame: true,
		},
		{
			name: "超预算_通用stub", content: bigSearch, tool: "message_search__search_by_range", maxChars: 20000,
			check: func(t *testing.T, out string) {
				var v map[string]any
				if err := json.Unmarshal([]byte(out), &v); err != nil {
					t.Fatalf("stub 应是合法 JSON: %v", err)
				}
				if v["omitted"] != true || v["tool"] != "message_search__search_by_range" {
					t.Fatalf("通用 stub 应标 omitted 与工具名: %s", out)
				}
				if strings.Contains(out, "结结结") {
					t.Fatal("不该留残片")
				}
				if !strings.Contains(v["hint"].(string), "message_search__search_by_range") {
					t.Fatalf("hint 要告诉模型怎么重新拿: %s", out)
				}
			},
		},
		{
			name: "小结果_原样", content: `{"success":true,"data":{"ok":1}}`, tool: "note__get", maxChars: 20000, wantSame: true,
		},
		{
			// data.html 是 null：不是整页结果，不该只凭「键存在」就换 stub
			name: "html为null不算命中", content: `{"success":true,"data":{"html":null}}`, tool: "html__get", maxChars: 20000, wantSame: true,
		},
		{
			name: "下发_maxChars为0_不走大小兜底", content: bigSearch, tool: "message_search__search_by_range", maxChars: 0, wantSame: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, replaced := StubToolResultForHistory(c.content, c.tool, c.maxChars)
			if c.wantSame {
				if replaced || out != c.content {
					t.Fatalf("应原样返回，实际 replaced=%v out=%.80s", replaced, out)
				}
				return
			}
			if !replaced {
				t.Fatalf("应换 stub，实际原样返回 %.80s", out)
			}
			c.check(t, out)
		})
	}
}

// 历史回放：assistant 行给出 tool_call_id → 函数名，tool 行据此换 stub；
// stubForHistory=false（replay 实验）保持全文。
func TestBuildMessagesForFlash_StubsToolResultForHistory(t *testing.T) {
	page := strings.Repeat("<p>正文</p>", 500)
	tcs := `[{"id":"call_1","type":"function","function":{"name":"html__get","arguments":"{}"}}]`
	history := []model.Message{
		{ID: "a", Role: "user", Content: "把第三段改一下"},
		{ID: "b", Role: "assistant", Content: "", ToolCallsJSON: &tcs},
		{ID: "c", Role: "tool", Content: htmlResultPage(page), ToolCallID: strPtr("call_1")},
	}

	msgs := BuildMessagesForFlash(history, 20000, true, true)
	if len(msgs) != 3 {
		t.Fatalf("消息数 %d", len(msgs))
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" {
		t.Fatalf("原生 FC 结构不能变: %+v", msgs[2])
	}
	if !strings.Contains(msgs[2].Content, `"html_omitted":true`) || strings.Contains(msgs[2].Content, `"html":`) {
		t.Fatalf("历史里应是 stub，实际 %.120s", msgs[2].Content)
	}

	plain := BuildMessagesForFlash(history, 20000, true, false)
	if !strings.Contains(plain[2].Content, `"html":`) {
		t.Fatal("stubForHistory=false 应保留全文")
	}
}

// 孤儿 tool 行（映射不到函数名）只按大小兜底，不让请求非法。
func TestBuildMessagesForFlash_OrphanToolFallsBackToSize(t *testing.T) {
	history := []model.Message{
		{ID: "c", Role: "tool", Content: strings.Repeat("x", 3000), ToolCallID: strPtr("call_missing")},
	}
	msgs := BuildMessagesForFlash(history, 2000, true, true)
	if !strings.Contains(msgs[0].Content, `"omitted":true`) {
		t.Fatalf("超预算的孤儿行应换通用 stub: %.80s", msgs[0].Content)
	}
}

// 一个 assistant 带多个 tool_calls、tool 行乱序：按 id 映射，不依赖顺序。
func TestBuildMessagesForFlash_MultipleToolCallsOutOfOrder(t *testing.T) {
	tcs := `[{"id":"call_html","function":{"name":"html__get","arguments":"{}"}},` +
		`{"id":"call_note","function":{"name":"note__get","arguments":"{}"}}]`
	note := `{"success":true,"data":{"id":"n1","content":"笔记正文"}}`
	history := []model.Message{
		{ID: "a", Role: "assistant", ToolCallsJSON: &tcs},
		{ID: "b", Role: "tool", Content: note, ToolCallID: strPtr("call_note")},
		{ID: "c", Role: "tool", Content: htmlResultPage(strings.Repeat("<p>正文</p>", 500)), ToolCallID: strPtr("call_html")},
	}
	msgs := BuildMessagesForFlash(history, 20000, true, true)

	if !strings.Contains(msgs[2].Content, `"html_omitted":true`) {
		t.Fatalf("按 id 应命中 html 行，实际 %.80s", msgs[2].Content)
	}
	if msgs[1].Content != note {
		t.Fatalf("另一个工具的结果不该被动: %.80s", msgs[1].Content)
	}
}

// 映射缺失（老数据 / 解析失败）的 html.get：走通用 stub，而不是头尾残片。
func TestBuildMessagesForFlash_UnmappedHTMLGetFallsBackToGenericStub(t *testing.T) {
	page := htmlResultPage(strings.Repeat("<p>正文</p>", 3000)) // > 20000 字符
	history := []model.Message{
		{ID: "c", Role: "tool", Content: page, ToolCallID: strPtr("call_unknown")},
	}
	msgs := BuildMessagesForFlash(history, 20000, true, true)
	if !strings.Contains(msgs[0].Content, `"omitted":true`) || strings.Contains(msgs[0].Content, `"html":`) {
		t.Fatalf("超预算且无映射应换通用 stub: %.120s", msgs[0].Content)
	}
}

// strPtr 测试用字符串指针。
func strPtr(s string) *string { return &s }
