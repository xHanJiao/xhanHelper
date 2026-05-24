package agent

import (
	"errors"
	"fmt"
	"testing"
)

func TestUserErrorMessage_DirectSentinel(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrPromptBuild, "服务繁忙，请重试"},
		{ErrLLMApi, "AI 服务暂时不可用，请重试"},
		{ErrMaxIterations, "任务未完成，请简化后重试"},
		{ErrInternal, "服务繁忙，请重试"},
		{ErrContextCancelled, "请求已取消"},
	}
	for _, tt := range tests {
		got := UserErrorMessage(tt.err)
		if got != tt.want {
			t.Errorf("UserErrorMessage(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestUserErrorMessage_Wrapped(t *testing.T) {
	// errors.Is 应穿透 fmt.Errorf 的 %w 包裹
	err := fmt.Errorf("theme state query: %w", ErrPromptBuild)
	got := UserErrorMessage(err)
	if got != "服务繁忙，请重试" {
		t.Errorf("expected '服务繁忙，请重试', got %q", got)
	}
}

func TestUserErrorMessage_Unknown(t *testing.T) {
	got := UserErrorMessage(errors.New("some random error"))
	if got != "服务繁忙，请重试" {
		t.Errorf("expected fallback '服务繁忙，请重试', got %q", got)
	}
}

func TestUserErrorMessage_Nil(t *testing.T) {
	got := UserErrorMessage(nil)
	if got != "服务繁忙，请重试" {
		t.Errorf("expected fallback for nil, got %q", got)
	}
}
