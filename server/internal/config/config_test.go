package config

import (
	"strings"
	"testing"

	"xhan-helper/internal/model"
)

func TestValidate_LLMBgAPIKey(t *testing.T) {
	base := Config{DB: DBConfig{Host: "localhost", Driver: "sqlite", Database: "test.db"}, LLM: LLMConfig{APIKey: "sk-main"}}
	cases := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "model 配置 + 未展开占位符 → 拒绝启动",
			cfg: func() *Config {
				c := base
				c.LLMBg = LLMConfig{Model: "deepseek-ai/DeepSeek-V4-Flash", APIKey: "${LLM_BG_API_KEY}"}
				return &c
			}(),
			wantErr: "LLM_BG_API_KEY",
		},
		{
			name: "model 配置 + 空 key → 拒绝启动",
			cfg: func() *Config {
				c := base
				c.LLMBg = LLMConfig{Model: "deepseek-ai/DeepSeek-V4-Flash"}
				return &c
			}(),
			wantErr: "LLM_BG_API_KEY",
		},
		{
			name: "model 配置 + 真实 key → 通过",
			cfg: func() *Config {
				c := base
				c.LLMBg = LLMConfig{Model: "deepseek-ai/DeepSeek-V4-Flash", APIKey: "sk-real-key"}
				return &c
			}(),
		},
		{
			name: "model 为空（回退主 LLM）→ 通过",
			cfg:  &base,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validate(c.cfg)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("expected error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestContextWindowFor(t *testing.T) {
	cfg := LLMConfig{
		Model:               "deepseek-v4-flash",
		StrongModel:         "deepseek-v4-pro",
		ContextWindow:       1000000,
		StrongContextWindow: 2000000,
	}
	cases := []struct {
		model string
		want  int
	}{
		{"deepseek-v4-pro", 2000000},   // 强档
		{"deepseek-v4-flash", 1000000}, // 弱档
		{"other-vendor-model", 1000000},
		{"", 1000000},
	}
	for _, c := range cases {
		if got := cfg.ContextWindowFor(c.model); got != c.want {
			t.Errorf("ContextWindowFor(%q) = %d, want %d", c.model, got, c.want)
		}
	}

	// 强档上限未配置（0）→ 回退弱档
	cfg.StrongContextWindow = 0
	if got := cfg.ContextWindowFor("deepseek-v4-pro"); got != 1000000 {
		t.Errorf("strong fallback = %d, want 1000000", got)
	}
}

// config 用字面量而非 model 常量（保持最底层不反向依赖 model），这里钉住两者一致。
func TestChunkDisabledParentsDefaultMatchesModel(t *testing.T) {
	cfg := &Config{
		DB:  DBConfig{Driver: "sqlite", Database: "test.db"},
		LLM: LLMConfig{APIKey: "sk-test"},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	want := []string{model.EntityTypeMemory, model.EntityTypeNote, model.EntityTypeSummary}
	got := cfg.Vector.ChunkDisabledParents
	if len(got) != len(want) {
		t.Fatalf("ChunkDisabledParents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ChunkDisabledParents[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
