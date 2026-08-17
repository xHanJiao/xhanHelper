package tool

import (
	"bytes"
	"fmt"
	"strings"
)

// catalogDescriptions — 渐进式工具目录的 caveman 风格组描述。
// 仅用于系统提示词目录展示（组名 + 描述 + 函数名列表），不改动各工具原 Description()
// （prompt-eng 模式、seeder embedding 等仍用原文）。缺省条目回退 Description()。
var catalogDescriptions = map[string]string{
	"bookkeeping":    "记账。加/改/查/删账单，月/分类统计，分类选项。",
	"datetime":       "时间。今天几号、当前时间。",
	"calculator":     "计算。四则运算、括号、sqrt/pow/sin/cos。",
	"note":           "笔记。记录/检索长期追踪信息：事实、观点、事件、状态、关系。",
	"document":       "文档。写/查/改/删 Markdown 文档，版本回滚。用户明确要求才用。",
	"agent":          "智能体。建/查/改自定义 AI 助手，绑定/解绑会话。",
	"reminder":       "提醒。设/查/取消待办。",
	"theme":          "主题管理。建/改/关/重启持续追踪事件（健身、求职）。",
	"theme_search":   "主题状态。列活跃主题、看状态页详情。",
	"get_summary":    "对话摘要。按时间/ID 查过去聊了什么。",
	"message_search": "历史消息。按时间/ID 精确查。语义检索走 unified_search。",
	"unified_search": "统一检索。语义/关键词搜记忆、笔记、摘要、主题、历史消息。",
	"plan":           "执行计划。存/读当前会话步骤清单（todo）。",
	"task":           "子任务。派子 agent 独立干活：联网调研 web_explore、本地文件 file_explore。",
	"webfetch":       "抓取网页正文。token 消耗大，仅读取页面原文时用。",
}

// ToolCatalog 生成工具组目录（组名 + 描述 + 函数名列表，不含参数 schema），
// 供渐进式工具暴露的初始提示词使用。输出按工具名排序，字节稳定（前缀缓存友好）。
// expose_tools 与 hidden 工具不进入目录。
func (r *Registry) ToolCatalog() string {
	var buf bytes.Buffer
	buf.WriteString("可用工具组（函数完整定义按需加载）。需要某组能力时先调用 expose_tools.enable([\"组名\"])，一次可多组，启用后下一轮即可直接调用其函数。\n")
	buf.WriteString("目录中的函数名可直接调用（参数简单时）；若参数结构复杂或不确定，先调用 expose_tools.enable 启用该组以获取完整参数定义，再调用其函数。\n\n")
	for _, name := range r.sortedVisibleToolNames() {
		if name == "expose_tools" {
			continue
		}
		t := r.tools[name]
		desc := catalogDescriptions[name]
		if desc == "" {
			desc = t.Description()
		}
		funcs := t.Functions()
		names := make([]string, len(funcs))
		for i, f := range funcs {
			names[i] = f.Name
		}
		buf.WriteString(fmt.Sprintf("- %s: %s\n", name, desc))
		buf.WriteString(fmt.Sprintf("  函数: %s\n", strings.Join(names, ", ")))
	}
	return buf.String()
}
