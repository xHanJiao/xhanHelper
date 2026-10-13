package agent

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// 抽取段：解析 {memories:[...]}、跳过空 summary、丢掉说不出 reason 的候选、按 MaxCandidates 截断。
func TestExtractCandidates_MultiAndTruncate(t *testing.T) {
	raw := `{"extracted":true,"memories":[
		{"summary":"a","facts":["f1"],"reason":"r-a"},
		{"summary":"","reason":"空 summary 跳过"},
		{"summary":"无理由","reason":""},
		{"summary":"b","reason":"r-b"},
		{"summary":"c","reason":"r-c"},
		{"summary":"d","reason":"r-d"},
		{"summary":"e","reason":"r-e"},
		{"summary":"f","reason":"r-f"}
	]}`
	result, err := parseExtractResult(raw)
	if err != nil || result == nil || !result.Extracted {
		t.Fatalf("should parse extracted=true: %v", err)
	}
	if len(result.Memories) != 8 {
		t.Fatalf("raw parse should keep 8, got %d", len(result.Memories))
	}

	// 走真实抽取路径：MaxCandidates=3，空 summary 跳过、无 reason 丢弃，截断到 3 条。
	mock := &syncMockLLM{responses: []llm.Message{{Content: raw}}}
	e := NewMemoryExtractor(&MemoryExtractorConfig{LLMClient: mock, MaxCandidates: 3})
	cands, err := e.ExtractCandidates(context.Background(), "u1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("truncate to 3, got %d", len(cands))
	}
	if cands[0].Summary != "a" || cands[0].Facts[0] != "f1" || cands[0].Reason != "r-a" {
		t.Errorf("first candidate fields wrong: %+v", cands[0])
	}
	for _, c := range cands {
		if c.Summary == "无理由" {
			t.Errorf("candidate without reason must be dropped: %+v", cands)
		}
	}
	// prompt 里的上限也应反映配置值，且必须要求 reason
	if !strings.Contains(mock.reqs[0].Messages[0].Content, "最多 3 条") {
		t.Errorf("prompt should state the configured cap, got: %.300s", mock.reqs[0].Messages[0].Content)
	}
	if !strings.Contains(mock.reqs[0].Messages[0].Content, "reason") {
		t.Errorf("prompt should require reason, got: %.300s", mock.reqs[0].Messages[0].Content)
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
	if err := validateMemoryFields("睡眠", "ok", nil); err != nil {
		t.Errorf("valid fields rejected: %v", err)
	}
	if err := validateMemoryFields("睡眠", "", nil); err == nil {
		t.Error("empty summary must fail")
	}
	if err := validateMemoryFields("", "ok", nil); err == nil {
		t.Error("empty topic must fail")
	}
	if err := validateMemoryFields("   ", "ok", nil); err == nil {
		t.Error("blank topic must fail")
	}
	if err := validateMemoryFields("六个汉字呀啊", "ok", nil); err == nil {
		t.Error("topic over 5 runes must fail")
	}
	if err := validateMemoryFields("五个汉", "ok", nil); err != nil {
		t.Errorf("5-rune topic must pass: %v", err)
	}
	if err := validateMemoryFields("Swift", "ok", nil); err != nil {
		t.Errorf("English topic must pass (中英文数字都合法): %v", err)
	}
	if err := validateMemoryFields("26计划", "ok", nil); err != nil {
		t.Errorf("topic with digits must pass: %v", err)
	}
	if err := validateMemoryFields("a/b", "ok", nil); err == nil {
		t.Error("topic with punctuation must fail")
	}
	if err := validateMemoryFields("求职-面试", "ok", nil); err == nil {
		t.Error("topic with dash must fail")
	}
	if err := validateMemoryFields("睡眠管理", "ok", nil); err != nil {
		t.Errorf("pure Chinese multi-word topic must pass: %v", err)
	}
	if err := validateMemoryFields("睡眠", joinRunes(long), nil); err == nil {
		t.Error("over-long summary must fail")
	}
	tooMany := make([]string, memoryFactsMaxCount+1)
	if err := validateMemoryFields("睡眠", "ok", tooMany); err != nil {
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

// clipTopic：保留中英文数字、剥掉标点空格、截到 ≤5 字；空输入返回空。
func TestClipTopic(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"   ":           "",
		"睡眠":            "睡眠",
		"WKWebView性能调优": "WKWeb", // 截到 5 字，英文保留
		"1234567":       "12345", // 数字保留
		"睡眠abc管理":       "睡眠abc", // 中英文都保留
		"求职-面试":         "求职面试",  // 剥掉标点
		"a/b c":         "abc",   // 剥掉标点空格
		"睡眠管理计划表":       "睡眠管理计", // 截到 5 字
	}
	for in, want := range cases {
		if got := clipTopic(in); got != want {
			t.Errorf("clipTopic(%q) = %q, want %q", in, got, want)
		}
	}
}

// rankNeighbors：按相似度取 top-K，再按引用次数降序展示。
func TestRankNeighbors(t *testing.T) {
	ns := []topicNeighbor{
		{Topic: "a", RefCount: 100, Similarity: 0.10},
		{Topic: "b", RefCount: 1, Similarity: 0.99},
		{Topic: "c", RefCount: 50, Similarity: 0.90},
	}
	got := rankNeighbors(append([]topicNeighbor{}, ns...))
	// 3 个都在 topK 内；展示按引用次数降序：a(100) → c(50) → b(1)。
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].Topic != "a" || got[1].Topic != "c" || got[2].Topic != "b" {
		t.Errorf("order wrong: %+v", got)
	}

	// 超过 topK：低相似度的被砍掉，且总数为 topK。
	var many []topicNeighbor
	for i := 0; i < topicNeighborTopK+5; i++ {
		many = append(many, topicNeighbor{Topic: "t", RefCount: int64(i), Similarity: float64(i) / 100})
	}
	gotMany := rankNeighbors(many)
	if len(gotMany) != topicNeighborTopK {
		t.Fatalf("want %d, got %d", topicNeighborTopK, len(gotMany))
	}
	// 展示首位应是 topK 内引用数最大者。
	if gotMany[0].RefCount != int64(topicNeighborTopK+4) {
		t.Errorf("top display wrong: %+v", gotMany[0])
	}
}

// buildTopicNamerInput：XML 良构；无近邻时省略 existing_topics；有近邻带引用数与相似度。
func TestBuildTopicNamerInput(t *testing.T) {
	none := buildTopicNamerInput("用户决定用方案B", nil)
	if !strings.Contains(none, "<memory>") || strings.Contains(none, "<topic ") {
		t.Errorf("no-neighbor input wrong: %s", none)
	}

	got := buildTopicNamerInput("用户决定用方案B", []topicNeighbor{
		{Topic: "项目规划", RefCount: 7, Similarity: 0.91},
	})
	for _, want := range []string{"<memory>", "用户决定用方案B", "项目规划", `ref_count="7"`, `similarity="0.91"`} {
		if !strings.Contains(got, want) {
			t.Errorf("namer input missing %q: %s", want, got)
		}
	}
	// 必须良构：summary 含 &<> 也要能被解析回来。
	raw := buildTopicNamerInput(`a & b <c> "d"`, []topicNeighbor{{Topic: "主题", RefCount: 1, Similarity: 0.5}})
	var parsed topicNamerInputXML
	if err := xml.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("namer input must be well-formed XML: %v\n%s", err, raw)
	}
	if parsed.Memory != `a & b <c> "d"` {
		t.Errorf("memory roundtrip failed: %q", parsed.Memory)
	}
	if len(parsed.Topics) != 1 || parsed.Topics[0].Value != "主题" {
		t.Errorf("topics roundtrip failed: %+v", parsed.Topics)
	}
}

// keepOrReplaceTopic：修「update 省略 topic 时把旧 topic 抹空」的 bug。
func TestKeepOrReplaceTopic(t *testing.T) {
	if got := keepOrReplaceTopic("", "旧主题"); got != "旧主题" {
		t.Errorf("empty incoming must keep old, got %q", got)
	}
	if got := keepOrReplaceTopic("   ", "旧主题"); got != "旧主题" {
		t.Errorf("blank incoming must keep old, got %q", got)
	}
	if got := keepOrReplaceTopic(" 新主题 ", "旧主题"); got != "新主题" {
		t.Errorf("incoming must win and be trimmed, got %q", got)
	}
}

// 写入路径 topic 优先级：nameTopic 定稿值(candTopic) > LLM 参数 > 旧值。
// 对应 saveMemory / updateMemory 里 keepOrReplaceTopic 的嵌套用法。
func TestTopicPrecedence(t *testing.T) {
	// save：candTopic 非空 → 用它，忽略 LLM 参数（哪怕 LLM 传了别的）。
	if got := keepOrReplaceTopic("定稿", "LLM乱填"); got != "定稿" {
		t.Errorf("candTopic must beat LLM arg, got %q", got)
	}
	// save：candTopic 为空 → 退回 LLM 参数。
	if got := keepOrReplaceTopic("", "LLM填的"); got != "LLM填的" {
		t.Errorf("empty candTopic must fall back to LLM arg, got %q", got)
	}
	// update：candTopic 空、LLM 也空 → 保留旧值（不抹空）。
	if got := keepOrReplaceTopic(keepOrReplaceTopic("", ""), "旧主题"); got != "旧主题" {
		t.Errorf("no topic anywhere must keep old, got %q", got)
	}
	// update：candTopic 非空 → 覆盖旧值。
	if got := keepOrReplaceTopic(keepOrReplaceTopic("定稿", ""), "旧主题"); got != "定稿" {
		t.Errorf("candTopic must override old, got %q", got)
	}
}

// validTopic：合规判定（trim 非空 + ≤5 字）。
func TestValidTopic(t *testing.T) {
	if _, ok := validTopic(""); ok {
		t.Error("empty must be invalid")
	}
	if _, ok := validTopic("  "); ok {
		t.Error("blank must be invalid")
	}
	if _, ok := validTopic("六个汉字呀啊"); ok {
		t.Error("6 runes must be invalid")
	}
	if got, ok := validTopic(" 睡眠 "); !ok || got != "睡眠" {
		t.Errorf("valid topic must be trimmed, got %q ok=%v", got, ok)
	}
}

// nameTopic：LLM 三次都不合规 → 截断兜底；第二次合规 → 采用第二次。
func TestNameTopic_RetryAndTruncate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}, &model.Note{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	deps := &RecordDeps{MemRepo: service.NewUserMemoryRepo(db), NoteRepo: service.NewNoteRepo(db)}

	// 三次都超长 → 用 fallback（原始 topic 截断）。
	mock := &syncMockLLM{responses: []llm.Message{
		{Content: "这是一个特别长的主题词"}, {Content: "还是很长的主题词啊"}, {Content: "依然超长主题词"},
	}}
	d := NewRecordDecisionLoop(deps, RecordLoopConfig{}, "post_chat")
	d.deps.FlashLLM = mock
	got := d.nameTopic(context.Background(), "u1", "原始主题词超长", "summary", []float64{1, 0})
	if got != "原始主题词" {
		t.Errorf("all-invalid must fall back to clipped topic, got %q", got)
	}
	if mock.callCount != 3 {
		t.Errorf("must try 3 times, got %d", mock.callCount)
	}

	// 第二次合规 → 采用第二次，不重试第三次。
	mock2 := &syncMockLLM{responses: []llm.Message{
		{Content: "超长主题词啊"}, {Content: "睡眠"},
	}}
	d2 := NewRecordDecisionLoop(deps, RecordLoopConfig{}, "post_chat")
	d2.deps.FlashLLM = mock2
	if got := d2.nameTopic(context.Background(), "u1", "", "summary", []float64{1, 0}); got != "睡眠" {
		t.Errorf("must take 2nd valid attempt, got %q", got)
	}
	if mock2.callCount != 2 {
		t.Errorf("must stop at 2nd, got %d calls", mock2.callCount)
	}

	// 三次都不合规 + 原始 topic/summary 只含标点（clip 后为空）→ 固定保底，绝不返回空。
	mock3 := &syncMockLLM{responses: []llm.Message{
		{Content: "a/b"}, {Content: "/"}, {Content: "- -"},
	}}
	d4 := NewRecordDecisionLoop(deps, RecordLoopConfig{}, "post_chat")
	d4.deps.FlashLLM = mock3
	if got := d4.nameTopic(context.Background(), "u1", "/", "- -", []float64{1, 0}); got != topicFallbackName {
		t.Errorf("all-invalid fallback must be %q, got %q", topicFallbackName, got)
	}
}

// sameTopicRecords：按 topic 精确取该主题已有记忆（总条数用于 count 属性），
// 已在语义近邻里的去重，topic 为空则不查。
func TestSameTopicRecords(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := service.NewUserMemoryRepo(db)
	mk := func(id, topic string) {
		m := &model.UserMemory{ID: id, UserID: "u1", MemoryKey: topic, ContentHash: id,
			MemoryValue: "内容-" + id, Topic: topic, Source: model.MemorySourcePostChat}
		if err := repo.Create(context.Background(), m); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("m1", "旅行计划")
	mk("m2", "旅行计划")
	mk("m3", "旅行计划")
	mk("other", "记账")

	d := NewRecordDecisionLoop(&RecordDeps{MemRepo: repo}, RecordLoopConfig{}, "post_chat")

	// m1 已在语义近邻里 → 不重复返回；总数仍是该主题的 3 条。
	got, total := d.sameTopicRecords(context.Background(), "u1", "旅行计划",
		[]recordNeighbor{{ID: "m1", Type: model.EntityTypeMemory}})
	if total != 3 {
		t.Errorf("total should be the whole topic size, got %d", total)
	}
	ids := map[string]bool{}
	for _, n := range got {
		if n.Type != model.EntityTypeMemory {
			t.Errorf("type must be memory, got %q", n.Type)
		}
		ids[n.ID] = true
	}
	if len(got) != 2 || ids["m1"] || !ids["m2"] || !ids["m3"] {
		t.Errorf("same-topic records wrong: %+v", got)
	}

	// topic 为空不查（返回零值，不报错）。
	if got, total := d.sameTopicRecords(context.Background(), "u1", "", nil); got != nil || total != 0 {
		t.Errorf("empty topic must skip the query, got %+v / %d", got, total)
	}
}

// sameTopicRecords 的降级路径：repo 报错时**不阻断决策**，退化成只有语义近邻（返回 nil, 0）。
func TestSameTopicRecords_DegradesOnRepoError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserMemory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("underlying db: %v", err)
	}
	if err := sqlDB.Close(); err != nil { // 关掉连接 → 后续查询必错
		t.Fatalf("close: %v", err)
	}

	d := NewRecordDecisionLoop(&RecordDeps{MemRepo: service.NewUserMemoryRepo(db)}, RecordLoopConfig{}, "post_chat")
	got, total := d.sameTopicRecords(context.Background(), "u1", "旅行计划", nil)
	if got != nil || total != 0 {
		t.Errorf("repo error must degrade to empty (not block the decision), got %+v / %d", got, total)
	}
}

// record-loop-prompt.md §2 的指令镜像是逐字复制，必须与代码一致。
// 这段指令曾经只在文档里、从未进代码——分家就是这个 bug 的成因，所以钉住它。
// 改代码时同步那份 md（或按 mirror 方式重新生成）。
func TestRecordLoopInstructionMirrorsDoc(t *testing.T) {
	const docPath = "../../../docs/design/memory-note-rework/record-loop-prompt.md"
	b, err := os.ReadFile(docPath)
	if err != nil {
		t.Skipf("设计文档不在此（非仓库内运行？）: %v", err)
	}
	want := "const recordLoopInstruction = `" + recordLoopInstruction + "`"
	if !strings.Contains(string(b), want) {
		t.Errorf("record-loop-prompt.md 的指令镜像与 recordLoopInstruction 不一致——改代码请同步该 md")
	}
}
