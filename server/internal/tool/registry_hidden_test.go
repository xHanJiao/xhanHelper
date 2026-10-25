package tool

import (
	"strings"
	"testing"
)

// hidden 语义测试：被 HideTool 的工具（当前是 profile / webfetch 时代遗留语义）仅 subAgent 可见
// 1) 主循环 LLM 可见面（原生 FC defs / prompt-eng prompt / tool_retriever 搜索）不含 hidden 工具
// 2) GetLLMToolsFiltered 按显式名单可导出 hidden 工具（subAgent 路径依赖）

func newHiddenTestRegistry() *Registry {
	r := NewRegistry()
	r.Register(&mockOrderTool{name: "visible_tool", desc: "可见工具", fns: []FuncSchema{
		{Name: "fn", Description: "函数"},
	}})
	r.Register(&mockOrderTool{name: "profile", desc: "画像查询工具", fns: []FuncSchema{
		{Name: "fetch_url", Description: "抓取 URL"},
	}})
	r.HideTool("profile")
	return r
}

func TestHiddenTool_ExcludedFromMainDefs(t *testing.T) {
	r := newHiddenTestRegistry()
	for _, def := range r.GetNativeFCTools() {
		if def.Name == "profile" {
			t.Error("profile 不应出现在 GetNativeFCTools（主循环原生 FC）")
		}
	}
	for _, name := range []string{"GeneratePrompt", "ToolOverview"} {
		var s string
		switch name {
		case "GeneratePrompt":
			s = r.GeneratePrompt()
		case "ToolOverview":
			s = r.ToolOverview()
		}
		if strings.Contains(s, "profile") {
			t.Errorf("%s 不应包含 profile（主循环 prompt-eng）", name)
		}
	}
}

func TestSearchTools_SkipsHidden(t *testing.T) {
	r := newHiddenTestRegistry()
	res := r.SearchTools("profile")
	if len(res) != 0 {
		t.Errorf("SearchTools 不应返回 hidden 工具: %+v", res)
	}
	res = r.SearchTools("抓取")
	for _, ti := range res {
		if ti.Name == "profile" {
			t.Error("SearchTools 按关键词搜索也不应返回 hidden 工具")
		}
	}
}

func TestGetLLMToolsFiltered_IncludesHidden(t *testing.T) {
	r := newHiddenTestRegistry()
	defs := r.GetLLMToolsFiltered([]string{"profile", "visible_tool"})
	if len(defs) != 2 {
		t.Fatalf("GetLLMToolsFiltered 应导出显式名单（含 hidden），got %d", len(defs))
	}
	if defs[0].Name != "profile" {
		t.Errorf("白名单顺序应保持: got %s", defs[0].Name)
	}
}

func TestGetLLMToolsFiltered_UnknownNameSkipped(t *testing.T) {
	r := newHiddenTestRegistry()
	defs := r.GetLLMToolsFiltered([]string{"profile", "browser", "visible_tool"})
	if len(defs) != 2 {
		t.Fatalf("未注册名（browser 等客户端工具）应跳过，got %d", len(defs))
	}
}

func TestExecuteByName_HiddenStillExecutable(t *testing.T) {
	r := newHiddenTestRegistry()
	res := r.ExecuteByName(&ExecuteContext{}, "profile", "fetch_url", nil)
	if !res.Success {
		t.Errorf("hidden 工具应仍可执行（subAgent 经 MiniReAct 调用）: %v", res.Error)
	}
}
