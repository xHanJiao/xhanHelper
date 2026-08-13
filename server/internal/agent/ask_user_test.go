package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"xhan-helper/internal/llm"
)

func TestAskUserDef_Structure(t *testing.T) {
	def := buildAskUserDef()
	if def.Name != "ui" {
		t.Fatalf("server name 应为 ui, got %s", def.Name)
	}
	if len(def.Functions) != 1 || def.Functions[0].Name != "ask_user" {
		t.Fatalf("应只含 ask_user 函数: %+v", def.Functions)
	}
	fn := def.Functions[0]
	if !strings.Contains(fn.Description, "验证码") || !strings.Contains(fn.Description, "换方案") {
		t.Errorf("函数描述应含用户介入规则: %s", fn.Description)
	}
	props, _ := fn.Parameters["properties"].(map[string]interface{})
	if _, ok := props["message"]; !ok {
		t.Errorf("message 参数必填缺失: %+v", fn.Parameters)
	}
	if _, ok := props["context"]; ok {
		t.Errorf("context 参数本期不做: %+v", fn.Parameters)
	}
}

func TestAppendAskUserTool_InjectedOnlyWithClientTools(t *testing.T) {
	base := []llm.ToolDefinition{{Name: "browser"}}
	// 客户端在线 → 注入
	defs, injected := appendAskUserTool(base, []ClientTool{{ServerName: "browser", Name: "navigate"}})
	if !injected || len(defs) != 2 || defs[1].Name != "ui" {
		t.Errorf("客户端在线应注入 ask_user: injected=%v defs=%+v", injected, defs)
	}
	// 客户端离线 → 不注入
	defs2, injected2 := appendAskUserTool(base, nil)
	if injected2 || len(defs2) != 1 {
		t.Errorf("客户端离线不应注入: injected=%v defs=%+v", injected2, defs2)
	}
}

func TestMiniReAct_AskUserBudget(t *testing.T) {
	m := NewMiniReAct(&MiniReActConfig{
		Client:       &blockingLLM{},
		ToolRegistry: noopRegistry(),
		SystemPrompt: "sys",
		UserID:       "u1",
		SubAgent: SubAgentExt{
			ClientToolTimeout: 120 * time.Second,
		},
	})

	// ui 工具：用 ctx 剩余时间（容差 100ms，time.Until 有微秒级损耗）
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if b := m.clientToolBudgetFor(ctx, "ui"); b < 29*time.Second || b > 30*time.Second {
		t.Errorf("ui 预算应为 ctx 剩余 ~30s, got %v", b)
	}

	// 无 deadline → 回退默认 120s
	if b := m.clientToolBudgetFor(context.Background(), "ui"); b != 120*time.Second {
		t.Errorf("无 deadline 应回退默认, got %v", b)
	}

	// 非 ui 工具：始终用默认预算
	if b := m.clientToolBudgetFor(ctx, "browser"); b != 120*time.Second {
		t.Errorf("browser 预算应始终用默认, got %v", b)
	}
}

func TestExplorePrompt_AskUserRules(t *testing.T) {
	s := explorePrompt
	for _, want := range []string{"ask_user", "验证码", "哪个页面", "不得重复请求"} {
		if !strings.Contains(s, want) {
			t.Errorf("explorePrompt 应含规则 %q", want)
		}
	}
}

func TestExploreWhitelist_ContainsUI(t *testing.T) {
	found := false
	for _, name := range exploreWhitelist {
		if name == "ui" {
			found = true
		}
	}
	if !found {
		t.Error("exploreWhitelist 应含 ui（ask_user 运行时 guard 放行）")
	}
}
