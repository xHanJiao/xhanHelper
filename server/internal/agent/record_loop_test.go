package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/tool"
)

// 抽取段：解析 {memories:[...]}、跳过空 summary、按 MaxCandidates 截断。
func TestExtractCandidates_MultiAndTruncate(t *testing.T) {
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

	// 走真实抽取路径：MaxCandidates=3，空 summary 跳过，截断到 3 条。
	mock := &syncMockLLM{responses: []llm.Message{{Content: raw}}}
	e := NewMemoryExtractor(&MemoryExtractorConfig{LLMClient: mock, MaxCandidates: 3})
	cands, err := e.ExtractCandidates(context.Background(), "u1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("truncate to 3, got %d", len(cands))
	}
	if cands[0].Topic != "t1" || cands[0].Facts[0] != "f1" {
		t.Errorf("first candidate fields wrong: %+v", cands[0])
	}
	// prompt 里的上限也应反映配置值
	if !strings.Contains(mock.reqs[0].Messages[0].Content, "最多 3 条") {
		t.Errorf("prompt should state the configured cap, got: %.300s", mock.reqs[0].Messages[0].Content)
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
	if err := validateMemoryFields("t", "ok", tooMany); err != nil {
		t.Errorf("count cap must not be enforced by validateMemoryFields (merged results are unbounded): %v", err)
	}
	if err := validateFactCount(tooMany); err == nil {
		t.Error("validateFactCount must reject over-cap raw input")
	}
}

// 三组名都要注册进临时 registry，否则 record_search / legacy_note 组查不到
// （provider 拼「组名__函数名」，MiniReAct 按组名查；只注册 memory 会报「未找到工具」）。
func TestRegisterRecordLoopTools_AllGroups(t *testing.T) {
	reg := tool.NewRegistry()
	registerRecordLoopTools(reg, &recordLoopState{})
	for _, name := range []string{recordToolSearch, recordToolMemory, recordToolLegacy} {
		if reg.Get(name) == nil {
			t.Errorf("group %q not registered", name)
		}
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

// mergeFacts：update 时 facts 只增不减——去重、保序、超限丢旧留新。
func TestMergeFacts(t *testing.T) {
	// 去重 + 保序（旧在前、新在后），trim 后相等算重复；大小写敏感。
	got := mergeFacts([]string{"A", "B", " C "}, []string{"C", "D"})
	want := []string{"A", "B", "C", "D"}
	if len(got) != len(want) {
		t.Fatalf("merge dedup/order: want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("merge[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

// mergeFacts：空入参。
func TestMergeFacts_Empty(t *testing.T) {
	if got := mergeFacts(nil, nil); len(got) != 0 {
		t.Errorf("nil+nil should be empty, got %v", got)
	}
	if got := mergeFacts(nil, []string{"A"}); len(got) != 1 || got[0] != "A" {
		t.Errorf("nil+[A] should be [A], got %v", got)
	}
	// 空/纯空白的 fact 不进结果。
	if got := mergeFacts([]string{"A"}, []string{"", "  "}); len(got) != 1 {
		t.Errorf("blank facts should be dropped, got %v", got)
	}
}

// mergeFacts：不逐出——合并结果可超上限，全部保留。
func TestMergeFacts_KeepsAllOverCap(t *testing.T) {
	old := make([]string, 0)
	for i := 0; i < memoryFactsMaxCount; i++ {
		old = append(old, string(rune('a'+i)))
	}
	got := mergeFacts(old, []string{"new1", "new2"})
	if len(got) != memoryFactsMaxCount+2 {
		t.Fatalf("no eviction expected: want %d, got %d", memoryFactsMaxCount+2, len(got))
	}
	// 旧的最前面和新加的都还在。
	if got[0] != "a" {
		t.Errorf("oldest fact should be kept, got %v", got)
	}
	if got[len(got)-1] != "new2" {
		t.Errorf("newest fact should be kept, got %v", got)
	}
}

// parseFactsPtr：factsPtr 的反向。空/坏 JSON 记日志后返回 nil（不阻断 update）。
func TestParseFactsPtr(t *testing.T) {
	p := factsPtr([]string{"A", "B"})
	if got := parseFactsPtr(p); len(got) != 2 || got[0] != "A" {
		t.Errorf("roundtrip failed: %v", got)
	}
	if got := parseFactsPtr(nil); got != nil {
		t.Errorf("nil should yield nil, got %v", got)
	}
	empty := ""
	if got := parseFactsPtr(&empty); got != nil {
		t.Errorf("empty string should yield nil, got %v", got)
	}
	bad := "not json"
	if got := parseFactsPtr(&bad); got != nil {
		t.Errorf("bad json should yield nil (logged), got %v", got)
	}
}
