package retrieval

import (
	"math"
	"testing"
)

// ===== bigramSet 测试 =====

func TestBigramSet_Empty(t *testing.T) {
	s := bigramSet("")
	if len(s) != 0 {
		t.Errorf("expected empty set, got %d bigrams", len(s))
	}
}

func TestBigramSet_SingleChar(t *testing.T) {
	s := bigramSet("你")
	if len(s) != 0 {
		t.Errorf("expected empty set for single char, got %d bigrams: %v", len(s), s)
	}
}

func TestBigramSet_TwoChars(t *testing.T) {
	s := bigramSet("你好")
	if len(s) != 1 {
		t.Fatalf("expected 1 bigram, got %d", len(s))
	}
	if !s["你好"] {
		t.Errorf("expected bigram '你好' in set: %v", s)
	}
}

func TestBigramSet_NormalChinese(t *testing.T) {
	s := bigramSet("今天天气真好")
	// "今天", "天天", "天气", "气真", "真好" = 5 bigrams
	if len(s) != 5 {
		t.Errorf("expected 5 bigrams, got %d: %v", len(s), s)
	}
	expected := []string{"今天", "天天", "天气", "气真", "真好"}
	for _, e := range expected {
		if !s[e] {
			t.Errorf("expected bigram %q in set", e)
		}
	}
}

func TestBigramSet_RepeatedBigrams(t *testing.T) {
	// "哈哈" has 2 identical chars, but only once since they're adjacent
	// "哈哈哈" has "哈哈" twice
	s := bigramSet("哈哈哈")
	if len(s) != 1 {
		t.Errorf("expected 1 unique bigram for '哈哈哈', got %d: %v", len(s), s)
	}
	if !s["哈哈"] {
		t.Errorf("expected bigram '哈哈' in set")
	}
}

func TestBigramSet_ASCII(t *testing.T) {
	s := bigramSet("hello")
	if len(s) != 4 {
		t.Errorf("expected 4 bigrams, got %d: %v", len(s), s)
	}
	expected := []string{"he", "el", "ll", "lo"}
	for _, e := range expected {
		if !s[e] {
			t.Errorf("expected bigram %q in set", e)
		}
	}
}

func TestBigramSet_MixedChineseASCII(t *testing.T) {
	s := bigramSet("A我很B")
	// "A我", "我很", "很B" = 3 bigrams
	if len(s) != 3 {
		t.Errorf("expected 3 bigrams, got %d: %v", len(s), s)
	}
}

// ===== bigramOverlap 测试 =====

func TestBigramOverlap_EmptyQueryBigrams(t *testing.T) {
	r := bigramOverlap(nil, "今天天气好")
	if r != 0 {
		t.Errorf("expected 0 for nil query bigrams, got %.4f", r)
	}
	r = bigramOverlap(map[string]bool{}, "今天天气好")
	if r != 0 {
		t.Errorf("expected 0 for empty query bigrams, got %.4f", r)
	}
}

func TestBigramOverlap_TargetTooShort(t *testing.T) {
	q := bigramSet("今天")
	if len(q) == 0 {
		t.Fatal("expected non-empty query set")
	}
	for _, target := range []string{"", "今"} {
		r := bigramOverlap(q, target)
		if r != 0 {
			t.Errorf("target=%q: expected 0, got %.4f", target, r)
		}
	}
}

func TestBigramOverlap_NoOverlap(t *testing.T) {
	q := bigramSet("今天")
	r := bigramOverlap(q, "hello")
	if r != 0 {
		t.Errorf("expected 0 for no overlap, got %.4f", r)
	}
}

func TestBigramOverlap_FullOverlap(t *testing.T) {
	q := bigramSet("今天天气")
	// query = {"今天","天天","天气"}, target same, overlap=3/3=1.0
	r := bigramOverlap(q, "今天天气")
	if r != 1.0 {
		t.Errorf("expected 1.0 for full overlap, got %.4f", r)
	}
}

func TestBigramOverlap_PartialOverlap(t *testing.T) {
	q := bigramSet("今天天气真好")
	// query = {"今天","天天","天气","气真","真好"} = 5
	// target = "今天下雨" → bigrams = {"今天","天下","下雨"}
	// overlap = "今天" only → 1/5 = 0.2
	r := bigramOverlap(q, "今天下雨")
	expected := 1.0 / 5.0
	if math.Abs(r-expected) > 0.0001 {
		t.Errorf("expected %.4f, got %.4f", expected, r)
	}
}

func TestBigramOverlap_AllBigramsMatch(t *testing.T) {
	// query = "你好", target = "你好世界" → target bigrams: "你好","好世","世界"
	// overlap = "你好" only → 1/1 = 1.0 (query has only 1 bigram)
	q := bigramSet("你好")
	r := bigramOverlap(q, "你好世界")
	if r != 1.0 {
		t.Errorf("expected 1.0 for all-query-bigrams-match, got %.4f", r)
	}
}

func TestBigramOverlap_MultipleOverlap(t *testing.T) {
	q := bigramSet("天气很好")
	// query = {"天气","气很","很好"}
	// target = "天气不错很好" → bigrams: {"天气","气不","不错","错很","很好"}
	// overlap: "天气" + "很好" = 2/3
	r := bigramOverlap(q, "天气不错很好")
	expected := 2.0 / 3.0
	if math.Abs(r-expected) > 0.0001 {
		t.Errorf("expected %.4f, got %.4f", expected, r)
	}
}

func TestBigramOverlap_ChineseEnglishMixed(t *testing.T) {
	q := bigramSet("hello世界")
	// query = {"he","el","ll","lo","o世","世界"} = 6
	// target = "hello" → {"he","el","ll","lo"} → overlap = 4
	r := bigramOverlap(q, "hello")
	expected := 4.0 / 6.0
	if math.Abs(r-expected) > 0.0001 {
		t.Errorf("expected %.4f, got %.4f", expected, r)
	}
}
