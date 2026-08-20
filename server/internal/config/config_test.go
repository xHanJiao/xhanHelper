package config

import (
	"strings"
	"testing"
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
