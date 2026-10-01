package model

import "testing"

func TestParseDocumentAudienceFilter(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"", DocumentAudienceFinished, true},
		{"finished", DocumentAudienceFinished, true},
		{"internal", DocumentAudienceInternal, true},
		{"all", "", true},
		{"  all  ", "", true},
		{"hidden", "", false},
	}
	for _, c := range cases {
		got, ok := ParseDocumentAudienceFilter(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("ParseDocumentAudienceFilter(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestValidDocumentAudience(t *testing.T) {
	for _, s := range []string{DocumentAudienceFinished, DocumentAudienceInternal} {
		if !ValidDocumentAudience(s) {
			t.Errorf("ValidDocumentAudience(%q) = false, want true", s)
		}
	}
	// all 是过滤哨兵，不是合法落库值
	for _, s := range []string{"", DocumentAudienceFilterAll, "hidden"} {
		if ValidDocumentAudience(s) {
			t.Errorf("ValidDocumentAudience(%q) = true, want false", s)
		}
	}
}
