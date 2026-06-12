package retrieval

import (
	"testing"
)

func TestExtractLatestFacts(t *testing.T) {
	tests := []struct {
		name       string
		stateFacts string
		n          int
		wantLen    int
		wantFirst  string
		wantLast   string
	}{
		{
			name:    "empty facts",
			n:       3,
			wantLen: 0,
		},
		{
			name: "fewer facts than n",
			stateFacts: `{"ts":"06-01","fact":"fact1"}
{"ts":"06-02","fact":"fact2"}`,
			n:       5,
			wantLen: 2,
		},
		{
			name: "more facts than n returns latest",
			stateFacts: `{"ts":"06-01","fact":"oldest"}
{"ts":"06-02","fact":"middle"}
{"ts":"06-03","fact":"newest"}
{"ts":"06-04","fact":"latest"}`,
			n:        3,
			wantLen:  3,
			wantLast: "latest",
		},
		{
			name: "blank lines skipped",
			stateFacts: `{"ts":"06-01","fact":"a"}

{"ts":"06-02","fact":"b"}`,
			n:       3,
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLatestFacts(tt.stateFacts, tt.n)
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			if tt.wantLen > 0 {
				if tt.wantLast != "" && got[len(got)-1] != tt.wantLast {
					t.Errorf("last = %q, want %q", got[len(got)-1], tt.wantLast)
				}
			}
		})
	}
}

func TestSearchThemes(t *testing.T) {
	vs := &mockVectorStore{
		results: []SearchResult{
			{ID: "t1", Score: 0.9, Metadata: map[string]string{"type": "theme", "user_id": "u1", "theme_id": "theme1"}},
			{ID: "t2", Score: 0.7, Metadata: map[string]string{"type": "theme", "user_id": "u1", "theme_id": "theme1"}},
			{ID: "t3", Score: 0.5, Metadata: map[string]string{"type": "theme", "user_id": "u1", "theme_id": "theme2"}},
			{ID: "t4", Score: 0.3, Metadata: map[string]string{"type": "theme", "user_id": "u2", "theme_id": "other_user"}},
		},
	}
	ts := &ThemeSource{vectorStore: vs}

	got := ts.searchThemes([]float64{0.1}, "u1", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 themes for u1, got %d: %v", len(got), got)
	}
	if got["theme1"] != 0.9 {
		t.Errorf("theme1 score = %.1f, want 0.9", got["theme1"])
	}
	if got["theme2"] != 0.5 {
		t.Errorf("theme2 score = %.1f, want 0.5", got["theme2"])
	}
}

func TestSearchChunks(t *testing.T) {
	vs := &mockVectorStore{
		results: []SearchResult{
			{ID: "c1", Score: 0.9, Metadata: map[string]string{"type": "chunk", "parent_type": "theme", "parent_id": "theme1", "user_id": "u1"}},
			{ID: "c2", Score: 0.8, Metadata: map[string]string{"type": "chunk", "parent_type": "theme", "parent_id": "theme1", "user_id": "u1"}},
			{ID: "c3", Score: 0.6, Metadata: map[string]string{"type": "chunk", "parent_type": "note", "parent_id": "n1", "user_id": "u1"}},
		},
	}
	ts := &ThemeSource{vectorStore: vs}

	got := ts.searchChunks([]float64{0.1}, "u1", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 theme from chunks, got %d", len(got))
	}
	if got["theme1"] != 0.9 {
		t.Errorf("theme1 score = %.1f, want 0.9", got["theme1"])
	}
}

func TestSearchAll(t *testing.T) {
	vs := &mockVectorStore{
		results: []SearchResult{
			{ID: "t1", Score: 0.7, Metadata: map[string]string{"type": "theme", "user_id": "u1", "theme_id": "a"}},
			{ID: "c1", Score: 0.9, Metadata: map[string]string{"type": "chunk", "parent_type": "theme", "parent_id": "a", "user_id": "u1"}},
			{ID: "t2", Score: 0.4, Metadata: map[string]string{"type": "theme", "user_id": "u1", "theme_id": "b"}},
		},
	}
	ts := &ThemeSource{vectorStore: vs}

	// curVec alone: theme hits a(0.7) chunk hits a(0.9)
	// histVec: theme hits a(0.7), b(0.4); chunk hits a(0.9)
	got := ts.searchAll([][]float64{{0.1}, {0.2}}, "u1", 10, 10)

	if got["a"] != 0.9 {
		t.Errorf("theme a score = %.1f, want 0.9 (chunk boost)", got["a"])
	}
	if got["b"] != 0.4 {
		t.Errorf("theme b score = %.1f, want 0.4", got["b"])
	}
}

func TestSearchAllEmptyVectors(t *testing.T) {
	vs := &mockVectorStore{}
	ts := &ThemeSource{vectorStore: vs}
	got := ts.searchAll(nil, "u1", 10, 10)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d entries", len(got))
	}
}
