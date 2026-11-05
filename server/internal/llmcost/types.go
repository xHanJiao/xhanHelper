package llmcost

import "context"

// ============================================================
// 大类常量（对应五大分类）
// ============================================================

const (
	CategoryMainChat   = "main_chat"  // 主聊天流
	CategoryPostChat   = "post_chat"  // 对话后处理
	CategoryReflection = "reflection" // 反思系统
	CategoryTool       = "tool"       // 工具内置
	CategoryAdmin      = "admin"      // 管理与评估
)

// ============================================================
// 子类常量（与设计文档一一对应；部分为历史场景，仅供读旧 cost 行）
// ============================================================

const (
	// main_chat
	SubReActLoop   = "re_act_loop"
	SubVerify      = "verify"
	SubTitle       = "title"
	SubSubAgentRun = "sub_agent_run" // 子 agent（task 工具）内部循环

	// post_chat
	SubMemoryExtract    = "memory_extract"
	SubEventAssemble    = "event_assemble"
	SubProfileExtract   = "profile_extract"
	SubProfileColdStart = "profile_cold_start"
	SubSummary          = "summary"
	SubImageDescribe    = "image_describe" // 上传后异步生成图片描述

	// reflection
	SubThemeDiscoverConv  = "theme_discover_conv"
	SubThemeDiscoverStats = "theme_discover_stats"
	SubThemeAnalyze       = "theme_analyze"
	SubStateVerify        = "state_verify"
	SubStateUpdate        = "state_update"

	// tool
	SubReminderGen  = "reminder_gen"
	SubRecordMemory = "record_memory" // 主聊天 record_memory 工具内含的记忆决策循环

	// admin
	SubAgentSummary = "agent_summary"
	SubReplay       = "replay"
	SubEvalPreLabel = "eval_prelabel"
)

// ============================================================
// Scenario — 场景标注，通过 context 传播给 CostTracker 装饰器
// ============================================================

type Scenario struct {
	Category    string                 // 必填，使用 CategoryXxx 常量
	SubCategory string                 // 必填，使用 SubXxx 常量
	UserID      string                 // 可选，覆盖 ChatRequest.UserID
	ConvID      string                 // 可选，会话 ID
	RefType     string                 // 可选，引用类型（finding/theme/mission/reminder/agent）
	RefID       string                 // 可选，引用 ID
	Meta        map[string]interface{} // 可选，其余扩展信息
}

// ============================================================
// context 传播
// ============================================================

type ctxKeyType struct{}

var ctxKey = ctxKeyType{}

// WithScenario 将 Scenario 注入 context.Context。
// 用法：
//
//	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{...})
//	client.Chat(ctx, req)
func WithScenario(ctx context.Context, s Scenario) context.Context {
	return context.WithValue(ctx, ctxKey, s)
}

// ScenarioFromContext 从 context 提取 Scenario。
// 返回 (Scenario, true) 如果有值，否则返回零值 + false。
func ScenarioFromContext(ctx context.Context) (Scenario, bool) {
	s, ok := ctx.Value(ctxKey).(Scenario)
	return s, ok
}
