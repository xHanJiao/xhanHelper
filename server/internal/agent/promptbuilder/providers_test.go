package promptbuilder

import (
	"testing"
)

func TestExtractLatestFactsForPrompt(t *testing.T) {
	tests := []struct {
		name       string
		stateFacts string
		n          int
		wantLen    int
		wantLast   string
	}{
		{
			name:    "empty facts",
			n:       3,
			wantLen: 0,
		},
		{
			name: "more facts than n returns latest with ts prefix",
			stateFacts: `{"ts":"06-01","fact":"oldest"}
{"ts":"06-02","fact":"middle"}
{"ts":"06-03","fact":"newest"}
{"ts":"06-04","fact":"latest"}`,
			n:        3,
			wantLen:  3,
			wantLast: "[06-04] latest",
		},
		{
			name:       "fewer facts than n",
			stateFacts: `{"ts":"06-01","fact":"only"}`,
			n:          3,
			wantLen:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLatestFactsForPrompt(tt.stateFacts, tt.n)
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			if tt.wantLast != "" && len(got) > 0 && got[len(got)-1] != tt.wantLast {
				t.Errorf("last = %q, want %q", got[len(got)-1], tt.wantLast)
			}
		})
	}
}
