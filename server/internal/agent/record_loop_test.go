package agent

import (
	"encoding/json"
	"testing"

	"xhan-helper/internal/tool"
)

// 抽取段：解析 {memories:[...]}、跳过空 summary、按上限截断。
func TestParseExtractResult_MultiAndTruncate(t *testing.T) {
	raw := `{"extracted":true,"memories":[
		{"summary":"a","topic":"t1","facts":["f1"]},
		{"summary":"","topic":"空"},
		{"summary":"b","topic":"t2"},
		{"summary":"c","topic":"t3"},
		{"summary":"d","topic":"t4"},
		{"summary":"e","topic":"t5"},
		{"summary":"f","topic":"t6"}
	]}`
	result, err := parseExtractResult(raw)
	if err != nil || result == nil || !result.Extracted {
		t.Fatalf("should parse extracted=true: %v", err)
	}
	if len(result.Memories) != 7 {
		t.Fatalf("raw parse should keep 7, got %d", len(result.Memories))
	}

	cands := make([]memoryCandidate, 0)
	for _, c := range result.Memories {
		if c.Summary == "" {
			continue
		}
		cands = append(cands, c)
		if len(cands) >= memoryMaxPerSession {
			break
		}
	}
	if len(cands) != memoryMaxPerSession {
		t.Fatalf("truncate to %d, got %d", memoryMaxPerSession, len(cands))
	}
	if cands[0].Topic != "t1" || cands[0].Facts[0] != "f1" {
		t.Errorf("first candidate fields wrong: %+v", cands[0])
	}
}

func TestParseExtractResult_NotExtracted(t *testing.T) {
	r, err := parseExtractResult(`{"extracted":false}`)
	if err != nil || r == nil || r.Extracted {
		t.Fatalf("extracted=false should not be extracted: %v", err)
	}
	if _, err := parseExtractResult(`not json at all`); err == nil {
		t.Fatal("garbage input must surface an error, not silently yield empty")
	}
}

// 目标校验：id 必须在近邻集内，且类型匹配（否则会删错表）。
func TestRecordLoopState_CheckTarget(t *testing.T) {
	s := &recordLoopState{
		idSet:  map[string]bool{"m1": true, "n1": true},
		idType: map[string]string{"m1": "memory", "n1": "note"},
	}
	if err := s.checkTarget("m1", "memory"); err != nil {
		t.Errorf("valid memory target should pass: %v", err)
	}
	if err := s.checkTarget("m1", "note"); err == nil {
		t.Error("type mismatch must fail (would delete wrong table)")
	}
	if err := s.checkTarget("ghost", "memory"); err == nil {
		t.Error("hallucinated id must fail")
	}
}

// 字段校验：topic / summary / facts 硬限制。
func TestValidateMemoryFields(t *testing.T) {
	long := make([]string, memorySummaryMaxLen+1)
	for i := range long {
		long[i] = "字"
	}
	if err := validateMemoryFields("t", "ok", nil); err != nil {
		t.Errorf("valid fields rejected: %v", err)
	}
	if err := validateMemoryFields("t", "", nil); err == nil {
		t.Error("empty summary must fail")
	}
	if err := validateMemoryFields("t", joinRunes(long), nil); err == nil {
		t.Error("over-long summary must fail")
	}
	tooMany := make([]string, memoryFactsMaxCount+1)
	if err := validateMemoryFields("t", "ok", tooMany); err == nil {
		t.Error("too many facts must fail")
	}
}

// 写工具识别：8 函数里哪 4 个是写入。
func TestIsWriteFunc(t *testing.T) {
	writes := []string{"save_memory", "update_memory", "delete_memory", "delete_note"}
	reads := []string{"search_memory_semantic", "search_memory_keyword", "search_note_semantic", "search_note_keyword"}
	for _, f := range writes {
		if !isWriteFunc(f) {
			t.Errorf("%s should be a write func", f)
		}
	}
	for _, f := range reads {
		if isWriteFunc(f) {
			t.Errorf("%s should NOT be a write func", f)
		}
	}
}

// extractResultID 解析工具返回的 {action,id}。
func TestExtractResultID(t *testing.T) {
	data, _ := json.Marshal(map[string]string{"action": "add", "id": "xyz"})
	if got := extractResultID(&tool.Result{Success: true, Data: data}); got != "xyz" {
		t.Errorf("want xyz, got %q", got)
	}
	if got := extractResultID(&tool.Result{Success: true, Data: json.RawMessage(`not json`)}); got != "" {
		t.Errorf("bad json should yield empty, got %q", got)
	}
}

// 关键词拆分：空格/逗号/顿号分隔 + 去重。
func TestSplitKeywordsDedup(t *testing.T) {
	got := splitKeywords("老王, 项目、老王 复盘")
	want := map[string]bool{"老王": true, "项目": true, "复盘": true}
	if len(got) != len(want) {
		t.Fatalf("want %d keywords, got %v", len(want), got)
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("unexpected keyword %q", k)
		}
	}
}

func joinRunes(rs []string) string {
	out := ""
	for _, r := range rs {
		out += r
	}
	return out
}
