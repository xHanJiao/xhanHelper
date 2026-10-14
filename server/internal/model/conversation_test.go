package model

import "testing"

func TestNormalizeConvMode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ConvModeWork}, // 旧数据可能缺列
		{ConvModeWork, ConvModeWork},
		{ConvModePlan, ConvModePlan},
		{"strict", ConvModePlan}, // 2026-10-14 撤档：历史严格档归 plan，绝不能落成不受限模式
		{"bogus", "bogus"},       // 未知值原样透传，由调用方判定
	}
	for _, c := range cases {
		if got := NormalizeConvMode(c.in); got != c.want {
			t.Errorf("NormalizeConvMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
