package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// explorePrompt — explore 子 agent 角色 prompt。
// 联网调研（webfetch/browser）只读专家；一次性完成调研后按固定结构输出结论。
const explorePrompt = `你是联网调研专家，擅长通过网页抓取与本地检索收集信息并提炼结论。

能力：
- 用 webfetch 抓取具体 URL 的正文（注意：结果全部进入上下文，token 消耗大——单页可达 5 万字符。只抓最有价值的一两个页面，优先汇总页/官方文档/百科，禁止逐页深挖）
- 用 browser 工具访问需要 JS 渲染的动态页面（需 Mac 客户端在线）
- 用 search_notes_v2 检索用户笔记（用户主动整理的知识库，可用于补充调研）
- 仅做只读操作，禁止写入任何数据
- 数据边界：用户的记忆、历史对话、摘要对子任务不可见，禁止尝试读取（没有对应工具）

工作准则：
- 先规划：明确调研目标、需要什么信息、从哪里获取
- 信息收集优先用高效来源（汇总页、官方文档、百科），避免逐页深挖
- 判断信息可信度：优先官方来源，标注不确定处
- 不要中途汇报进度，一次性完成全部调研后输出结论

过程记录（重要，每个页面打开都必须记录）：
- 每次打开页面记录一行结果：成功读全文 / 超时 / HTTP 403 / 验证码拦截 / 网络不通——不允许只列计划不列实际结果
- 同一域名失败满 2 次 → 判定此站被墙，不再尝试该域名下的任何页面
- 同一任务累计 4 次网络类失败（超时/断连/网络不通）→ 立即停止开页，把失败清单交回主任务
- 页面打开超时/加载失败 → 换源，不要反复重试
- tab 节制：默认 background 打开；读完即 close_tab 清理；同一时刻活跃标签不超过 3 个；配额满时先 close_tab 腾出配额再打开
- 标签归属：多个子任务共享浏览器，list_tabs 结果里每个标签带 owner 标注——只操作 owner 为自己（subagent）的标签，**禁止关闭其他任务的标签**（close_tab 会被服务端拒绝，报错里会说明归属）

页面拦截与止损（重要）：
- 识别结构性拦截：页面标题含 "Attention Required"/Cloudflare、HTTP 403/404 等 → 该站不可达，立即换源，禁止重试同一 URL

标注纪律：
- 训练知识兜底必须声明知识截止时间（约 2025 年初），仅作背景参考，不能当结论用
- 时点性数据（stars/版本号/价格/交易额）查不到实时值一律标"未核实"，禁止从训练记忆估计数字
- 搜索摘要/二手转述最高标"中"置信度；只有直接读一手来源（官网/论文/仓库 README/releases）才能标"高"
- 多个来源口径冲突时必须在要点里并列说明，禁止只挑一个当结论

用户介入（ask_user 工具）：
- 仅在必须用户介入时调用（验证码、登录墙等），一次只请求一个动作
- message 必须指明哪个页面/tab（如『XX 网站的验证码，请处理』）——多子任务并发时用户才能区分
- 用户跳过或超时后必须换方案（换来源/跳过该页），不得重复请求同一动作

最终输出必须严格使用以下结构（不要在结构外输出任何文字）：
<research_result>
<summary>3-5 句话的总体结论</summary>
<points>要点列表，每条不超过 50 字，用分号分隔</points>
<sources>成功来源清单：标题 | URL | 置信度(高/中/低)</sources>
<failed_attempts>失败的访问尝试清单：URL | 失败类型（超时/403/验证码/网络不通），没有则填"无"</failed_attempts>
<open_questions>未解决的问题（没有则填"无"）</open_questions>
</research_result>`

// exploreWhitelist — explore 子 agent 可用工具白名单。
// 含服务端工具名与客户端工具 server_name（browser）；与定义过滤（GetLLMToolsFiltered）
// 和运行时 guard（MiniReAct AllowedTools）双层拦截一致。
//
// 数据边界：仅保留笔记检索（search_notes_v2——用户主动整理的知识库）。
// 记忆/历史对话/摘要（unified_search、search_memories_v2、message_search）对子任务
// 不可见——子任务不应自动读取用户的私密对话数据。
var exploreWhitelist = []string{
	"webfetch",
	"browser",
	"ui", // 用户介入（ask_user），仅客户端在线时注入 defs（见 appendAskUserTool）
	"search_notes_v2",
	"datetime",
	"calculator",
}

// whitelistMap 将白名单切片转为 map（MiniReAct AllowedTools 运行时 guard 用）。
func whitelistMap(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// filterClientToolsByWhitelist 按 ToolDefinition.Name（= server_name）过滤客户端工具定义。
func filterClientToolsByWhitelist(defs []llm.ToolDefinition, whitelist []string) []llm.ToolDefinition {
	wl := whitelistMap(whitelist)
	filtered := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if wl[d.Name] {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// buildSubAgentSystemPrompt 构建子 agent 的简化 system prompt（时间 + 角色）。
// 工具定义不进 prompt：原生 FC 下经 MiniReActConfig.Tools 传入。
func buildSubAgentSystemPrompt() string {
	var buf strings.Builder
	buf.WriteString("你是 xhanHelper 的子 agent。\n")
	buf.WriteString(fmt.Sprintf("当前时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
	buf.WriteString(explorePrompt)
	buf.WriteString("\n\n")
	return buf.String()
}

// subAgentMsgSink — MiniReAct MsgSink 实现：子循环消息完整落库到子会话（可观测/可调试）。
type subAgentMsgSink struct {
	msgRepo *service.MessageRepo
	convID  string
	userID  string
}

func (s *subAgentMsgSink) create(m *model.Message) {
	m.IsSubAgent = true // 子会话消息标记：按用户扫描的后台统计/检索聚合统一排除（见 message_repo.go subConversationExclusion）
	if err := s.msgRepo.Create(context.Background(), m); err != nil {
		log.Printf("[subagent] save msg error (conv=%s): %v", s.convID[:8], err)
	}
}

func (s *subAgentMsgSink) SaveUserMsg(content string) {
	s.create(&model.Message{
		ID:             uuid.New().String(),
		ConversationID: s.convID,
		UserID:         s.userID,
		Role:           "user",
		Content:        content,
	})
}

func (s *subAgentMsgSink) SaveAssistantMsg(msg llm.Message) {
	m := &model.Message{
		ID:             uuid.New().String(),
		ConversationID: s.convID,
		UserID:         s.userID,
		Role:           "assistant",
		Content:        msg.Content,
	}
	if len(msg.ToolCalls) > 0 {
		b, err := json.Marshal(msg.ToolCalls)
		if err == nil {
			s := string(b)
			m.ToolCallsJSON = &s
		}
	}
	if msg.ReasoningContent != "" {
		rc := msg.ReasoningContent
		m.ReasoningContent = &rc
	}
	s.create(m)
}

func (s *subAgentMsgSink) SaveToolMsg(toolCallID, content string) {
	tcid := toolCallID
	s.create(&model.Message{
		ID:             uuid.New().String(),
		ConversationID: s.convID,
		UserID:         s.userID,
		Role:           "tool",
		Content:        content,
		ToolCallID:     &tcid,
		IsInternal:     true,
	})
}

// RunSubAgent 实现 tool.SubAgentRunner：创建子会话 → MiniReAct 独立循环 →
// 结果（含超时/失败的部分成果）返回给 task 工具。
func (o *Orchestrator) RunSubAgent(ctx context.Context, opts *tool.SubAgentRunOptions) *tool.SubAgentRunResult {
	// 步骤 1 — 深度检查（写死 ≤1 层：主会话可派子会话，子会话不可再派）
	depth := 0
	currentID := opts.ParentConvID
	for {
		parent, err := o.subConvRepo.FindByID(ctx, currentID)
		if err != nil || parent == nil || parent.ParentID == nil {
			break
		}
		depth++
		currentID = *parent.ParentID
	}
	if depth >= 1 {
		return &tool.SubAgentRunResult{
			Success: false,
			Error:   "已达到子任务深度限制（1层）。当前会话已是子任务，不可再派生于任务。请直接完成该调研。",
		}
	}

	// 步骤 2 — 获取主会话 UserID
	mainConv, err := o.subConvRepo.FindByID(ctx, opts.ParentConvID)
	if err != nil || mainConv == nil {
		return &tool.SubAgentRunResult{Success: false, Error: "无法获取父会话信息"}
	}
	userID := mainConv.UserID

	// 步骤 3 — 创建子 Conversation（列表不可见：ListByUser 已过滤 parent_id IS NULL）
	extraJSON, _ := json.Marshal(&model.ConversationExtra{SubAgentType: opts.SubAgentType})
	subConv := &model.Conversation{
		ID:       uuid.New().String(),
		UserID:   userID,
		Title:    fmt.Sprintf("%s (%s)", opts.Description, opts.SubAgentType),
		ParentID: &opts.ParentConvID,
		Extra:    string(extraJSON),
		Mode:     model.ConvModeWork,
	}
	if err := o.subConvRepo.Create(ctx, subConv); err != nil {
		return &tool.SubAgentRunResult{Success: false, Error: fmt.Sprintf("创建子会话失败: %v", err)}
	}

	// 步骤 4 — 转换 ClientTools（无客户端桥时不下发客户端工具，explore 退化为仅 webfetch）
	agentClientTools := toClientTools(opts.ClientTools)
	clientBridge := o.clientBridge
	if clientBridge == nil {
		agentClientTools = nil
	}

	// 步骤 5 — 构建工具定义（服务端 + 客户端分别按白名单过滤）
	tools := o.registry.GetLLMToolsFiltered(exploreWhitelist)
	tools = append(tools, filterClientToolsByWhitelist(buildClientToolDefs(agentClientTools, o.registry), exploreWhitelist)...)
	// ask_user 注入：仅当客户端在线（ClientTools 非空）时——无浏览器场景 ask_user 无意义。
	// 注意：def 必须同时进入 tools（LLM 可见）与 ClientTools（MiniReAct clientIdx 分发索引），
	// 缺一不可——只加 def 会导致 LLM 能调用但分发路由不到客户端（"未找到工具 'ui'"）。
	tools, agentClientTools = appendAskUserTool(tools, agentClientTools)

	// 步骤 6 — 构造 MiniReAct（复用既有组件，含强制收敛轮/工具日志/事件回调）
	maxIter := o.subAgentMaxIterations
	if maxIter <= 0 {
		maxIter = 20 // 兜底防死循环（timeout 才是真实预算）
	}
	react := NewMiniReAct(&MiniReActConfig{
		Client:        o.flashClient, // 子 agent 固定 Flash
		Tools:         tools,
		ToolRegistry:  o.registry,
		SystemPrompt:  buildSubAgentSystemPrompt(),
		UserID:        userID,
		ConvID:        subConv.ID,
		MaxIterations: maxIter, // 主循环 maxIter-1 轮 + 1 轮强制收敛（MiniReAct 内置）
		Caller:        "subagent:" + opts.SubAgentType,
		SubAgent: SubAgentExt{
			ClientTools:       agentClientTools,
			ClientBridge:      clientBridge,
			ClientToolTimeout: o.clientToolTimeout,
			AllowedTools:      whitelistMap(exploreWhitelist),
			MsgSink:           &subAgentMsgSink{msgRepo: o.subMsgRepo, convID: subConv.ID, userID: userID},
		},
		OnEvent: o.makeSubAgentForwarder(opts.MainEvents, subConv.ID, opts.Description),
	})

	// 子任务活动面板：start 事件
	if opts.MainEvents != nil {
		o.sendEvent(opts.MainEvents, &llm.StreamEvent{Type: "subagent", Content: subAgentEventJSON(subConv.ID, opts.Description, "start", "", "started")})
	}

	// 步骤 8 — 超时控制与执行（成本中心：子循环 LLM 调用记 main_chat/sub_agent_run）
	timeout := o.subAgentTimeout
	if timeout <= 0 {
		timeout = 600 * time.Second
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	subCtx = llmcost.WithScenario(subCtx, llmcost.Scenario{
		Category:    llmcost.CategoryMainChat,
		SubCategory: llmcost.SubSubAgentRun,
		UserID:      userID,
		ConvID:      subConv.ID,
		Meta: map[string]interface{}{
			"subagent_type": opts.SubAgentType,
			"description":   opts.Description,
		},
	})

	// 子任务结束（completed/timeout/failed 三态）：
	// 1) 发 end 事件给活动面板；2) 关闭本次循环打开的全部浏览器标签（只清自己的）。
	var result *MiniReActResult
	endState := "completed"
	endSummary := ""
	defer func() {
		if opts.MainEvents != nil {
			o.sendEvent(opts.MainEvents, &llm.StreamEvent{Type: "subagent", Content: subAgentEventJSON(subConv.ID, opts.Description, "end", endSummary, endState)})
		}
		if o.clientBridge != nil && result != nil && len(result.ToolCallLog) > 0 {
			o.cleanupSubAgentTabs(opts.MainEvents, userID, subConv.ID, result.ToolCallLog)
		}
	}()

	result, err = react.Run(subCtx, opts.Prompt)
	if err == nil {
		endState = "completed"
		endSummary = truncateToolResult(result.FinalContent, 200)
		return &tool.SubAgentRunResult{
			Success: true,
			State:   "completed",
			Text:    result.FinalContent,
			ConvID:  subConv.ID,
		}
	}

	// 步骤 9 — 超时/失败部分结果组装（不丢调研成果）
	state := "failed"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(subCtx.Err(), context.DeadlineExceeded) {
		state = "timeout"
	}
	endState = state
	endSummary = truncateToolResult(summarizeToolCallLog(result.ToolCallLog), 200)
	return &tool.SubAgentRunResult{
		Success: false,
		State:   state,
		Error:   fmt.Sprintf("子任务%s: %v", state, err),
		Text:    summarizeToolCallLog(result.ToolCallLog),
		ConvID:  subConv.ID,
	}
}

// toClientTools 将 tool.ClientToolInfo 转换为 agent.ClientTool。
func toClientTools(infos []tool.ClientToolInfo) []ClientTool {
	if len(infos) == 0 {
		return nil
	}
	out := make([]ClientTool, len(infos))
	for i, ct := range infos {
		out[i] = ClientTool{
			ServerName:  ct.ServerName,
			Name:        ct.Name,
			Description: ct.Description,
			Parameters:  ct.Parameters,
			IsReadOnly:  ct.IsReadOnly,
		}
	}
	return out
}

// makeSubAgentForwarder 构造子循环事件 → 主 SSE 的转发器。
// 子任务活动面板（SubagentPanel）专用：统一发 subagent 事件（JSON 含 conv_id/description/
// type/content），客户端按 conv_id 归集；client_tool_call 保持原通道阻塞送达（浏览器指令 +
// ask_user 必须到客户端）。
// 返回 bool：false 仅用于 client_tool_call 转发超时（5s 未送达）——MiniReAct mustSend
// 据此立即失败，不再白等整个客户端工具预算（并行任务高事件率下事件可能因 channel 满被丢）。
// 子循环执行期间主循环阻塞于 wg.Wait，无串台风险。
func (o *Orchestrator) makeSubAgentForwarder(mainEvents chan<- *llm.StreamEvent, convID, description string) func(*llm.StreamEvent) bool {
	if mainEvents == nil {
		// replay/测试场景：无主 SSE，全部丢弃
		return func(*llm.StreamEvent) bool { return true }
	}
	return func(evt *llm.StreamEvent) bool {
		switch evt.Type {
		case "client_tool_call":
			select {
			case mainEvents <- evt:
				return true
			case <-time.After(5 * time.Second):
				log.Printf("[subagent] client_tool_call forward timeout (main SSE full?)")
				return false
			}
		case "status", "tool_call", "tool_result":
			o.sendEvent(mainEvents, &llm.StreamEvent{Type: "subagent", Content: subAgentEventJSON(convID, description, evt.Type, evt.Content, "")})
		case "mini_react:status":
			o.sendEvent(mainEvents, &llm.StreamEvent{Type: "subagent", Content: subAgentEventJSON(convID, description, "status", evt.Content, "")})
		case "mini_react:tool_call", "mini_react:tool_result":
			o.sendEvent(mainEvents, &llm.StreamEvent{Type: "subagent", Content: subAgentEventJSON(convID, description, strings.TrimPrefix(evt.Type, "mini_react:"), evt.Content, "")})
			// mini_react:thought / mini_react:done / mini_react:error → 丢弃（不暴露子循环内部）
		}
		return true
	}
}

// subAgentEvent — 子任务活动面板 SSE 事件。
// type: start | end | status | tool_call | tool_result；start/end 带 state。
type subAgentEvent struct {
	ConvID      string `json:"conv_id"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Content     string `json:"content"`
	State       string `json:"state,omitempty"`
}

func subAgentEventJSON(convID, description, evtType, content, state string) string {
	b, _ := json.Marshal(subAgentEvent{
		ConvID: convID, Description: description,
		Type: evtType, Content: content, State: state,
	})
	return string(b)
}

// summarizeToolCallLog 将子循环工具调用日志组装为摘要文本（超时/失败时回传主 LLM 的部分成果）。
// 每条：tool.function → 成功/失败 + 结果截断 ~500 字符；总预算 ~4000 字符。
func summarizeToolCallLog(logs []ToolCallLog) string {
	if len(logs) == 0 {
		return "（子任务提前结束，尚未产生工具调用结果）"
	}
	const perLine = 500
	const totalBudget = 4000
	var buf strings.Builder
	for i, tc := range logs {
		line := fmt.Sprintf("[%d] %s.%s → ", i+1, tc.Tool, tc.Function)
		if tc.Result != nil && tc.Result.Success {
			line += "成功: " + truncateToolResult(string(tc.Result.Data), perLine)
		} else if tc.Result != nil {
			line += "失败: " + truncateToolResult(tc.Result.Error, perLine)
		} else {
			line += "无结果"
		}
		buf.WriteString(line)
		buf.WriteString("\n")
		if buf.Len() >= totalBudget {
			break
		}
	}
	return truncateToolResult(buf.String(), totalBudget)
}

// collectTabIDsFromLog 从子循环工具调用日志收集浏览器 open 成功结果的 tabId（去重）。
func collectTabIDsFromLog(logs []ToolCallLog) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, tc := range logs {
		if tc.Tool != "browser" || tc.Function != "agent_browser_open" {
			continue
		}
		if tc.Result == nil || !tc.Result.Success {
			continue
		}
		var m struct {
			TabID string `json:"tabId"`
		}
		if err := json.Unmarshal(tc.Result.Data, &m); err != nil || m.TabID == "" {
			continue
		}
		if !seen[m.TabID] {
			seen[m.TabID] = true
			ids = append(ids, m.TabID)
		}
	}
	return ids
}

// cleanupSubAgentTabs 子任务结束时逐个关闭本次循环打开的浏览器标签。
// 复用客户端工具协议（close_tab），独立短超时 ctx（单 tab 上限由 subAgentCleanupTimeout
// 控制，不占子循环预算）；失败（tab 已关/客户端断连）忽略，客户端 housekeeping 兜底。
func (o *Orchestrator) cleanupSubAgentTabs(mainEvents chan<- *llm.StreamEvent, userID string, convID string, logs []ToolCallLog) {
	tabIDs := collectTabIDsFromLog(logs)
	if len(tabIDs) == 0 {
		return
	}
	timeout := o.subAgentCleanupTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	log.Printf("[subagent] cleanup: closing %d tabs", len(tabIDs))
	for _, tabID := range tabIDs {
		args, _ := json.Marshal(map[string]string{"tabId": tabID})
		_, _ = o.executeClientTool(ctx, mainEvents, userID,
			tool.ToolCall{Tool: "browser", Function: "agent_browser_close_tab", Args: args}, model.ConvModeWork, "subagent:"+convID)
	}
}
