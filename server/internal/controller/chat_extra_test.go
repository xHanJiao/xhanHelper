package controller

import "testing"

func TestPinnedEventIDsFromExtra(t *testing.T) {
	cases := []struct {
		name  string
		extra string
		want  []string
	}{
		{name: "empty", extra: "", want: nil},
		{name: "empty object", extra: "{}", want: nil},
		{name: "no event focus", extra: `{"used_tool_ids":["x"]}`, want: nil},
		{name: "pinned", extra: `{"event_focus":{"enabled":["e1","e2"]}}`, want: []string{"e1", "e2"}},
		{name: "broken json", extra: `{"event_focus":`, want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pinnedEventIDsFromExtra(c.extra)
			if len(got) != len(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %+v, want %+v", got, c.want)
				}
			}
		})
	}
}
