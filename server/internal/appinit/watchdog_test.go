package appinit

import "testing"

func TestShouldExit(t *testing.T) {
	cases := []struct {
		name           string
		start, current int
		want           bool
	}{
		{"启动时就没有真实父进程", 1, 1, false},
		{"父进程还在", 1234, 1234, false},
		{"被托孤给 launchd", 1234, 1, true},
		{"被重新托孤给别的进程", 1234, 5678, true},
	}
	for _, c := range cases {
		if got := shouldExit(c.start, c.current); got != c.want {
			t.Errorf("%s: shouldExit(%d,%d)=%v, want %v", c.name, c.start, c.current, got, c.want)
		}
	}
}
