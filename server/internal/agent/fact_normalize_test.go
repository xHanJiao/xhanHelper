package agent

import "testing"

func TestNormalizeFactContent(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		same bool
	}{
		{"中文标点", "本人只做技术。", "本人只做技术", true},
		{"全角与空格", "ＡＢＣ 项目", "abc项目", true},
		{"大小写", "Rust 学习", "rust学习", true},
		{"下划线与冒号", "due_at: 2026-09-21", "dueat2026-09-21", true},
		{"日期不同必须不同", "2026-09-18 和老K吵架", "2026-09-25 和老K吵架", false},
		{"改口必须不同", "本人只做技术", "本人兼产品与开发", false},
		{"数字不同必须不同", "欠款 3 万", "欠款 5 万", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hashPrompt(normalizeFactContent(c.a)) == hashPrompt(normalizeFactContent(c.b))
			if got != c.same {
				t.Errorf("same=%v, want %v: %q vs %q → %q / %q",
					got, c.same, c.a, c.b, normalizeFactContent(c.a), normalizeFactContent(c.b))
			}
		})
	}
}

func TestNormalizeFactContent_Empty(t *testing.T) {
	for _, s := range []string{"", "   ", "，。！？——", "\u3000"} {
		if got := normalizeFactContent(s); got != "" {
			t.Errorf("normalize(%q) = %q, want empty", s, got)
		}
	}
}
