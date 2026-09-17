package controller

import (
	"strings"
	"testing"

	"xhan-helper/internal/agent/retrieval"
)

func TestNormalizeDocQuote(t *testing.T) {
	if got := normalizeDocQuote(nil); got != "" {
		t.Errorf("nil 引用应通过: %q", got)
	}

	// 空白折叠（客户端发的本来就是折叠文本，这里兜底防脏数据）
	q := &retrieval.DocQuoteMeta{Quote: "第一句\n\n   第二句\u3000第三句"}
	if got := normalizeDocQuote(q); got != "" {
		t.Fatalf("合法引用应通过: %q", got)
	}
	if q.Quote != "第一句 第二句 第三句" {
		t.Errorf("quote 应折叠空白: %q", q.Quote)
	}

	// 前后文只截断不报错：prefix 保尾部（靠近选区）、suffix 保头部
	q = &retrieval.DocQuoteMeta{Quote: "引用", Prefix: strings.Repeat("前", 100), Suffix: strings.Repeat("后", 100)}
	if got := normalizeDocQuote(q); got != "" {
		t.Fatalf("超长前后文不应报错: %q", got)
	}
	if n := len([]rune(q.Prefix)); n != maxAnchorHintLen {
		t.Errorf("prefix 应截到 %d 字，实际 %d", maxAnchorHintLen, n)
	}
	if n := len([]rune(q.Suffix)); n != maxAnchorHintLen {
		t.Errorf("suffix 应截到 %d 字，实际 %d", maxAnchorHintLen, n)
	}
	if !strings.HasSuffix(q.Prefix, "前") || !strings.HasPrefix(q.Suffix, "后") {
		t.Errorf("截断方向错了: prefix=%q suffix=%q", q.Prefix, q.Suffix)
	}

	// 标题超长直接截断，不打断整轮提问
	q = &retrieval.DocQuoteMeta{Quote: "引用", Title: strings.Repeat("标", maxDocQuoteTitleLen+50)}
	if got := normalizeDocQuote(q); got != "" {
		t.Fatalf("超长标题不应报错: %q", got)
	}
	if n := len([]rune(q.Title)); n != maxDocQuoteTitleLen {
		t.Errorf("title 应截到 %d 字，实际 %d", maxDocQuoteTitleLen, n)
	}
}

func TestNormalizeDocQuote_Rejects(t *testing.T) {
	if got := normalizeDocQuote(&retrieval.DocQuoteMeta{Quote: "   \n  "}); got == "" {
		t.Error("空 quote 应报错")
	}
	long := strings.Repeat("字", maxAnnotationQuoteLen+1)
	if got := normalizeDocQuote(&retrieval.DocQuoteMeta{Quote: long}); got == "" {
		t.Error("超长 quote 应报错")
	}
	ok := strings.Repeat("字", maxAnnotationQuoteLen)
	if got := normalizeDocQuote(&retrieval.DocQuoteMeta{Quote: ok}); got != "" {
		t.Errorf("刚好 %d 字应通过: %q", maxAnnotationQuoteLen, got)
	}
}
