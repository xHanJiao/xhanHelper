package config

import (
	"strings"
	"testing"

	"xhan-helper/internal/model"
)

// merge 对 bool 必须「显式才覆盖」：env 文件没写的键保持 base，写了 false 也要能压过 base true。
func TestMerge_BoolFieldsExplicitOnly(t *testing.T) {
	yes, no := true, false

	// env 项缺失 → base 原样保留（尤其 base 的 false 不能被刷掉）
	base := Config{}
	base.Server.MemoryAutoExtract = &no
	base.Server.DisableBackgroundTasks = &no
	base.SubAgent.UserContextEnabled = &yes
	merge(&base, &Config{})
	if base.Server.AutoExtractMemory() {
		t.Error("absent memory_auto_extract must keep base false")
	}
	if base.Server.BackgroundTasksDisabled() {
		t.Error("absent disable_background_tasks must keep base false")
	}
	if !base.SubAgent.PromptUserContext() {
		t.Error("absent user_context_enabled must keep base true")
	}

	// env 显式 false 必须能压过 base true（反向：显式 true 压过 base false）
	base2 := Config{}
	base2.Server.MemoryAutoExtract = &no
	base2.Server.DisableBackgroundTasks = &no
	base2.SubAgent.UserContextEnabled = &yes
	env := Config{}
	env.Server.MemoryAutoExtract = &yes
	env.Server.DisableBackgroundTasks = &yes
	env.SubAgent.UserContextEnabled = &no
	merge(&base2, &env)
	if !base2.Server.AutoExtractMemory() {
		t.Error("explicit true must override base false")
	}
	if !base2.Server.BackgroundTasksDisabled() {
		t.Error("explicit true must override base false")
	}
	if base2.SubAgent.PromptUserContext() {
		t.Error("explicit false must override base true")
	}
}

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

// activity.enabled 默认开（nil → true）；显式 false 才关。
func TestActivityEnabledDefaultOn(t *testing.T) {
	var c ActivityConfig
	if !c.IsEnabled() {
		t.Error("nil enabled must default to true")
	}
	no := false
	c.Enabled = &no
	if c.IsEnabled() {
		t.Error("explicit false must disable")
	}
	yes := true
	c.Enabled = &yes
	if !c.IsEnabled() {
		t.Error("explicit true must enable")
	}

	// merge：env 写了 false 能压过 base 的缺省（nil）
	base := Config{}
	env := Config{}
	env.Activity.Enabled = &no
	merge(&base, &env)
	if base.Activity.IsEnabled() {
		t.Error("env explicit false must override base default-on")
	}
}

// message.extra 检索 trace 保留默认 14 天。
func TestDebugJSONExtraTraceRetentionDefault(t *testing.T) {
	cfg := &Config{DB: DBConfig{Driver: "sqlite", Database: "test.db"}, LLM: LLMConfig{APIKey: "k"}}
	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := cfg.LLM.DebugJSON.ExtraTraceRetentionDays; got != 14 {
		t.Errorf("默认应为 14, got %d", got)
	}
}

// merge：env 缺省不覆盖 base；非零（含负数关闭）覆盖。
func TestMergeDebugJSONExtraTraceRetention(t *testing.T) {
	base := Config{}
	base.LLM.DebugJSON.ExtraTraceRetentionDays = 14
	merge(&base, &Config{})
	if base.LLM.DebugJSON.ExtraTraceRetentionDays != 14 {
		t.Error("env 缺省不应覆盖 base")
	}
	merge(&base, &Config{LLM: LLMConfig{DebugJSON: DebugJSONConfig{ExtraTraceRetentionDays: 3}}})
	if base.LLM.DebugJSON.ExtraTraceRetentionDays != 3 {
		t.Errorf("env 非零应覆盖, got %d", base.LLM.DebugJSON.ExtraTraceRetentionDays)
	}
	merge(&base, &Config{LLM: LLMConfig{DebugJSON: DebugJSONConfig{ExtraTraceRetentionDays: -1}}})
	if base.LLM.DebugJSON.ExtraTraceRetentionDays != -1 {
		t.Errorf("负数应覆盖(关闭), got %d", base.LLM.DebugJSON.ExtraTraceRetentionDays)
	}
}

// activity.watermark_mode：空 = time；merge 只在非空时覆盖。
func TestActivityWatermarkMode(t *testing.T) {
	var c ActivityConfig
	if got := c.WatermarkModeNormalized(); got != "time" {
		t.Errorf("空值应归一为 time, got %q", got)
	}
	c.WatermarkMode = "segment"
	if got := c.WatermarkModeNormalized(); got != "segment" {
		t.Errorf("got %q", got)
	}

	base := Config{Activity: ActivityConfig{WatermarkMode: "segment"}}
	env := Config{}
	merge(&base, &env)
	if base.Activity.WatermarkMode != "segment" {
		t.Errorf("env 空值不应覆盖 base, got %q", base.Activity.WatermarkMode)
	}
	env2 := Config{Activity: ActivityConfig{WatermarkMode: "time"}}
	merge(&base, &env2)
	if base.Activity.WatermarkMode != "time" {
		t.Errorf("env 非空应覆盖, got %q", base.Activity.WatermarkMode)
	}
}

// 抽取段温度用 != nil 判据覆盖：0（贪心）是合法取值，不能被当成「没写」。
func TestMerge_MemoryExtractTemperature(t *testing.T) {
	zero, hot := 0.0, 0.7

	base := Config{}
	base.Server.MemoryExtractTemperature = &hot
	merge(&base, &Config{}) // env 没写 → 保留 base
	if base.Server.MemoryExtractTemperature == nil || *base.Server.MemoryExtractTemperature != hot {
		t.Errorf("absent key must keep base, got %v", base.Server.MemoryExtractTemperature)
	}

	env := Config{}
	env.Server.MemoryExtractTemperature = &zero
	merge(&base, &env)
	if base.Server.MemoryExtractTemperature == nil || *base.Server.MemoryExtractTemperature != 0 {
		t.Errorf("explicit 0 must override base, got %v", base.Server.MemoryExtractTemperature)
	}
}
