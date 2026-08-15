package agent

import (
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
)

func TestSimpleUserContent_PrefersOriginalContent(t *testing.T) {
	orig := "原始输入"
	content := "处理后的内容"
	m := &model.Message{
		Content:         content,
		OriginalContent: &orig,
		CreatedAt:       time.Date(2026, 8, 12, 14, 30, 0, 0, config.BJ),
	}
	out := SimpleUserContent(m)
	if !strings.HasPrefix(out, "[08-12 14:30] ") {
		t.Errorf("expected time prefix, got %q", out)
	}
	if !strings.Contains(out, orig) {
		t.Errorf("should use original content, got %q", out)
	}
	if strings.Contains(out, content) {
		t.Errorf("should NOT use processed content, got %q", out)
	}
}

func TestSimpleUserContent_FallsBackToContent(t *testing.T) {
	m := &model.Message{
		Content:   "管线处理后的消息",
		CreatedAt: time.Date(2026, 8, 12, 9, 5, 0, 0, config.BJ),
	}
	out := SimpleUserContent(m)
	if !strings.Contains(out, "管线处理后的消息") {
		t.Errorf("should fall back to Content, got %q", out)
	}
}

func TestSimpleUserContent_NoXMLContext(t *testing.T) {
	// 无 XMLContext 的消息也正常输出（原逻辑直接走 Content）
	m := &model.Message{Content: "普通消息", CreatedAt: time.Date(2026, 8, 12, 0, 0, 0, 0, config.BJ)}
	out := SimpleUserContent(m)
	if !strings.HasPrefix(out, "[08-12 00:00] ") {
		t.Errorf("expected prefix, got %q", out)
	}
}
