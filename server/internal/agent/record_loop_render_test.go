package agent

import (
	"encoding/xml"
	"strings"
	"testing"
)

// 候选输入块：三段（candidate / neighbors / topic_records）都要良构，空块也是合法结构，id 不带前缀。
// 渲染走 xml.Marshal —— 顺带验证控制字符会被拒（QueryEscape 时代会漏出去）。
func TestRenderCandidateInput(t *testing.T) {
	out, err := renderCandidateInput(
		recordCandidate{Topic: "睡眠", Summary: "早睡", Facts: []string{"23:00"}},
		[]recordNeighbor{{ID: "abc", Type: "memory", Topic: "睡眠", Content: "旧记忆"}},
		[]recordNeighbor{{ID: "def", Type: "memory", Topic: "睡眠", Content: "更旧的记忆"}},
		32,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(out, "<candidate>") || !containsStr(out, `id="abc"`) || !containsStr(out, `id="def"`) {
		t.Errorf("unexpected render: %s", out)
	}
	if !containsStr(out, `count="32"`) {
		t.Errorf("topic_records 必须带该主题总条数（治碎片化的信号）: %s", out)
	}
	// 三段都要能被回读（良构）
	candPart, rest, ok := strings.Cut(out, "\n<neighbors>")
	if !ok {
		t.Fatalf("missing neighbors block:\n%s", out)
	}
	nbPart, tpPart, ok := strings.Cut(rest, "\n<topic_records")
	if !ok {
		t.Fatalf("missing topic_records block:\n%s", out)
	}
	if err := xml.Unmarshal([]byte(candPart), new(candidateXML)); err != nil {
		t.Errorf("candidate not well-formed: %v\n%s", err, candPart)
	}
	if err := xml.Unmarshal([]byte("<neighbors>"+nbPart), new(neighborsXML)); err != nil {
		t.Errorf("neighbors not well-formed: %v\n%s", err, out)
	}
	var tp topicRecordsXML
	if err := xml.Unmarshal([]byte("<topic_records"+tpPart), &tp); err != nil {
		t.Errorf("topic_records not well-formed: %v\n%s", err, out)
	}
	if tp.Count != 32 || len(tp.Records) != 1 || tp.Records[0].ID != "def" {
		t.Errorf("topic_records roundtrip wrong: %+v", tp)
	}

	empty, err := renderCandidateInput(recordCandidate{Summary: "x"}, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(empty, "<neighbors></neighbors>") {
		t.Errorf("empty neighbors block wrong: %s", empty)
	}
	if !containsStr(empty, "<topic_records") || !containsStr(empty, `count="0"`) {
		t.Errorf("empty topic_records block wrong: %s", empty)
	}

	// 控制字符（0x0B）不是合法 XML 字符：xml.Marshal 会替换成 U+FFFD（不报错、不产生非良构输出）。
	// 这条钉住「不能原样漏进 prompt」——旧的手拼 xmlEscape 会把它漏出去。
	ctrl, err := renderCandidateInput(recordCandidate{Summary: "a\x0bb"}, nil, nil, 0)
	if err != nil {
		t.Fatalf("xml.Marshal replaces control chars, should not error: %v", err)
	}
	if strings.ContainsRune(ctrl, '\x0b') {
		t.Errorf("control char leaked into prompt input:\n%s", ctrl)
	}
	if err := xml.Unmarshal([]byte(ctrl), new(candidateXML)); err != nil {
		t.Errorf("control-char input not well-formed: %v", err)
	}
}

// 决策循环的 system prompt 必须真的带指令——它曾经整段漏实现（只有 3 句门面话），
// 模型因此不知道「什么都不调」是合法终局、也没有「同一件事」的判据。见设计文档 §2。
func TestRecordLoopSystemPrompt_HasInstruction(t *testing.T) {
	for _, want := range []string{
		"<topic_records>", // 同主题视野
		"什么都不调",           // NONE 是合法终局
		"同一件事 = 同一主体",     // 判据
		"delete 是最后手段",    // 删除闸门
		"target_id 只能取自输入里的 id",
	} {
		if !strings.Contains(recordLoopSystemPrompt, want) {
			t.Errorf("system prompt 缺关键节 %q", want)
		}
	}
	if !strings.Contains(recordLoopSystemPrompt, "5 字") {
		t.Error("topic 上限必须按实现写 5 字（文档里的 20 字是旧的）")
	}
}
