package agent

import (
	"encoding/xml"
	"strings"
	"testing"
)

// 候选输入块：neighbors 为空也要是合法结构，且 id 不带前缀。
// 渲染走 xml.Marshal —— 顺带验证控制字符会被拒（QueryEscape 时代会漏出去）。
func TestRenderCandidateInput(t *testing.T) {
	out, err := renderCandidateInput(
		recordCandidate{Topic: "睡眠", Summary: "早睡", Facts: []string{"23:00"}},
		[]recordNeighbor{{ID: "abc", Type: "memory", Topic: "睡眠", Content: "旧记忆"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(out, "<candidate>") || !containsStr(out, `id="abc"`) {
		t.Errorf("unexpected render: %s", out)
	}
	// 两段都要能被回读（良构）
	candPart, nbPart, ok := strings.Cut(out, "\n<neighbors>")
	if !ok {
		t.Fatalf("missing neighbors block:\n%s", out)
	}
	if err := xml.Unmarshal([]byte(candPart), new(candidateXML)); err != nil {
		t.Errorf("candidate not well-formed: %v\n%s", err, candPart)
	}
	if err := xml.Unmarshal([]byte("<neighbors>"+nbPart), new(neighborsXML)); err != nil {
		t.Errorf("neighbors not well-formed: %v\n%s", err, out)
	}

	empty, err := renderCandidateInput(recordCandidate{Summary: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(empty, "<neighbors></neighbors>") {
		t.Errorf("empty neighbors block wrong: %s", empty)
	}

	// 控制字符（0x0B）不是合法 XML 字符：xml.Marshal 会替换成 U+FFFD（不报错、不产生非良构输出）。
	// 这条钉住「不能原样漏进 prompt」——旧的手拼 xmlEscape 会把它漏出去。
	ctrl, err := renderCandidateInput(recordCandidate{Summary: "a\x0bb"}, nil)
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
