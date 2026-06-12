package agent

import (
	"testing"
)

func TestParseHistoryToFacts(t *testing.T) {
	history := `--- 2026-05-19 20:53 ---

--- 2026-05-19 20:56 ---
## 主题状态
1. [2026-05-19] 用户完成简历制作
2. [2026-05-19] 梳理出6个深挖点

--- 2026-05-21 09:01 ---
1. [2026-05-19] 用户完成简历制作
2. [2026-05-21] 开始准备面试介绍`

	facts := parseHistoryToFacts(history)
	if len(facts) < 2 {
		t.Fatalf("expected at least 2 facts from deduped history, got %d", len(facts))
	}

	// 同一事实 "用户完成简历制作" 出现两次，去重后只保留一条
	resumeCount := 0
	for _, f := range facts {
		if f.Fact == "用户完成简历制作" {
			resumeCount++
		}
	}
	if resumeCount != 1 {
		t.Errorf("expected 1 deduped '用户完成简历制作', got %d", resumeCount)
	}

	// 验证时间戳正确
	hasInterview := false
	for _, f := range facts {
		if f.Fact == "开始准备面试介绍" && f.Ts == "2026-05-21" {
			hasInterview = true
		}
	}
	if !hasInterview {
		t.Error("missing fact '开始准备面试介绍' with ts 2026-05-21")
	}
}

func TestParseHistoryToFactsEmpty(t *testing.T) {
	facts := parseHistoryToFacts("")
	if len(facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(facts))
	}
}

func TestParseHistoryToFactsShortFacts(t *testing.T) {
	history := `--- 2026-05-19 20:53 ---
1. [2026-05-19] ok
2. [2026-05-19] 够长的事实
3. [2026-05-19] no`

	facts := parseHistoryToFacts(history)
	// "ok" and "no" are < 4 chars, filtered out
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact (short ones filtered), got %d", len(facts))
	}
	if facts[0].Fact != "够长的事实" {
		t.Errorf("got %q, want %q", facts[0].Fact, "够长的事实")
	}
}

func TestDeduplicateFacts(t *testing.T) {
	facts := []FactEntry{
		{Ts: "06-01", Fact: "用户完成简历制作"},
		{Ts: "06-02", Fact: "用户完成简历制作"},
		{Ts: "06-01", Fact: "开始准备面试"},
		{Ts: "06-03", Fact: " 用户完成简历制作 "}, // whitespace trimmed, same
	}
	got := deduplicateFacts(facts)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique facts, got %d", len(got))
	}
	var factsList []string
	for _, f := range got {
		factsList = append(factsList, f.Fact)
	}
	hasResume := false
	hasInterview := false
	for _, f := range factsList {
		if f == "用户完成简历制作" {
			hasResume = true
		}
		if f == "开始准备面试" {
			hasInterview = true
		}
	}
	if !hasResume || !hasInterview {
		t.Errorf("missing expected facts, got: %v", factsList)
	}
}
