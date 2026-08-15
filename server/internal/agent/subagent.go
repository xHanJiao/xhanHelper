package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// webExplorePrompt — web_explore 子 agent 角色 prompt。
// Mac 浏览器（browser）只读调研专家；一次性完成调研后按固定结构输出结论。
const webExplorePrompt = `你是联网调研专家，擅长通过浏览器访问网页收集信息并提炼结论。

能力：
- 用 browser 工具访问需要 JS 渲染的动态页面（需 Mac 客户端在线）
- 用 unified_search 检索用户笔记（用户主动整理的知识库，可用于补充调研）
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

// fileExplorePrompt — file_explore 子 agent 角色 prompt。
// 本地文件检索（fs__glob/grep/read）只读专家；一次性完成检索后按固定结构输出结论。
const fileExplorePrompt = `你是本地文件检索专家，擅长在用户的 Mac 文件系统上快速定位文件、搜索内容并提炼结论。
你的输出将直接交给主 agent 汇总回答用户，不直接面向用户。

能力（只读）：
- fs__glob：按文件名模式找文件（pattern 支持 "**/*.go" 等，path 为绝对路径起步目录）
- fs__grep：正则搜索文件内容，返回文件+行号+匹配行
- fs__read：读文件（每行带行号前缀）或列目录；offset/limit 分页读大文件
- fs__read 支持 PDF：按页提取纯文本，page_offset（起始页，默认 1）/page_limit（最多页数，默认 10）续读；
  每页带 [Page X of N] 标记；大 PDF 先读前 10 页摸清结构，再按 page_offset 定位关键页细读，不要一次读满
- 扫描件 PDF（无文本层）无法提取文本，报错后如实告知主任务，不要假装读完
- datetime / calculator：辅助

工作准则：
- 先规划：明确找什么、在哪找（用户主目录、项目目录等）
- 定位顺序：glob 找文件 → grep 定位行 → read 读上下文。不要上来就 read 大文件
- 多文件独立读取要并行调用（一次回复多个工具调用），不要串行等待
- 避免小块切割读取（30 行一读）；需要更多上下文就一次读更大范围
- 结果被截断（有 "Output capped"/"Results truncated"/"Use skip=N to continue" 提示）→
  换更精确 pattern/path，或 read 用 offset 续读、grep 用 skip 参数翻下一页（不要重复同一 pattern 不加 skip）
- 搜索路径无授权时会触发用户确认；被拒绝后换方案，不要重复请求同一路径
- 遇到二进制文件/超大文件，用 grep 定位而不是 read

安全与边界：
- 严禁写入/修改/删除任何文件（没有写工具）
- 敏感文件（.env、密钥、证书等）会被客户端拦截，属预期，不要尝试绕过
- 数据边界：用户的笔记、记忆、历史对话对子任务不可见（没有对应工具）

最终输出必须严格使用以下结构（不要在结构外输出任何文字）：
<file_explore_result>
<summary>3-5 句话的总体结论</summary>
<findings>发现清单，每条：文件绝对路径 + 行号（file:line）+ 内容要点，用分号分隔</findings>
<files_read>已读文件清单（绝对路径），没读填"无"</files_read>
<open_questions>未解决的问题（没有则填"无"）</open_questions>
</file_explore_result>`

// subagentSpec — 子 agent 类型定义表（prompt / 白名单 / 结果注入预算 / 客户端要求）。
// RunSubAgent 按 subagent_type 查表分派：prompt、服务端白名单、客户端函数级白名单、
// MiniReAct maxToolResult、客户端硬要求。新增类型 = 表里加一行，骨架零改动。
type subagentSpec struct {
	prompt          string              // 角色 prompt（buildSubAgentSystemPrompt 用）
	serverWhitelist []string            // 服务端工具名（registry.GetLLMToolsFiltered）
	clientWhitelist map[string][]string // 客户端 server_name → 允许函数名；nil=该组全部函数
	maxToolResult   int                 // MiniReAct 工具结果注入上限字符数；0=默认 2000
	requiresClient  bool                // 客户端硬要求：无 macOS 客户端在线直接报错，不降级
}

// subagentSpecs — 子 agent 类型注册表。数据边界说明：
//   - web_explore：浏览器（browser）+ 笔记检索（unified_search——用户主动整理的知识库）。
//     记忆/历史对话/摘要对子任务不可见。webfetch 已移除（单页 5 万字符全量进上下文、
//     token 消耗大且被 2000 字符截断破坏——用户决策）。
//   - file_explore：仅本地文件只读工具（fs 的 glob/grep/read，写工具函数级剥除）。
//     browser 不注入；ui（ask_user 用户介入）保留；笔记/记忆均不可见（子任务不应自动读取用户私密数据）。
var subagentSpecs = map[string]subagentSpec{
	"web_explore": {
		prompt:          webExplorePrompt,
		serverWhitelist: []string{"unified_search", "datetime", "calculator"},
		clientWhitelist: map[string][]string{"browser": nil, "ui": nil},
		maxToolResult:   2000,
		requiresClient:  true,
	},
	"file_explore": {
		prompt:          fileExplorePrompt,
		serverWhitelist: []string{"datetime", "calculator"},
		clientWhitelist: map[string][]string{"fs": {"glob", "grep", "read"}, "ui": nil},
		// 对齐客户端注入预算（clientToolInjectBudgetBytes）：fs_read 单页 ≈19KB，
		// 2000 默认会把读文件能力砍到 1/10
		maxToolResult:  clientToolInjectBudgetBytes,
		requiresClient: true,
	},
}

// buildAllowedTools 将 spec 的服务端+客户端白名单合并为 MiniReAct 运行时 guard 索引。
// 服务端工具整组放行（nil funcs）；客户端按 spec.clientWhitelist 的函数粒度限制。
func buildAllowedTools(spec subagentSpec) map[string][]string {
	m := make(map[string][]string, len(spec.serverWhitelist)+len(spec.clientWhitelist))
	for _, n := range spec.serverWhitelist {
		m[n] = nil
	}
	for name, funcs := range spec.clientWhitelist {
		m[name] = funcs
	}
	return m
}

// filterClientToolsByFuncWhitelist 按 server_name → 允许函数名 过滤客户端工具定义。
// funcs 为 nil 表示该 server 全部函数放行；否则逐函数过滤（剥掉 fs__write/fs__edit 等）。
// 与运行时 guard（MiniReAct AllowedTools 函数级）双层拦截一致。
func filterClientToolsByFuncWhitelist(defs []llm.ToolDefinition, wl map[string][]string) []llm.ToolDefinition {
	if len(defs) == 0 || len(wl) == 0 {
		return nil
	}
	filtered := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		funcs, ok := wl[d.Name]
		if !ok {
			continue
		}
		if funcs == nil {
			filtered = append(filtered, d)
			continue
		}
		kept := make([]llm.FuncDef, 0, len(d.Functions))
		for _, f := range d.Functions {
			if slices.Contains(funcs, f.Name) {
				kept = append(kept, f)
			}
		}
		if len(kept) == 0 {
			continue
		}
		d.Functions = kept
		filtered = append(filtered, d)
	}
	return filtered
}

// clientToolGroupNames 提取客户端白名单的 server 组名（错误信息用）。
// ui（ask_user 用户介入）是可选注入的，不列为必需工具组。
func clientToolGroupNames(wl map[string][]string) string {
	names := make([]string, 0, len(wl))
	for n := range wl {
		if n == "ui" {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// buildSubAgentSystemPrompt 构建子 agent 的简化 system prompt（时间 + 类型角色 + 固定预算）。
// 工具定义不进 prompt：原生 FC 下经 MiniReActConfig.Tools 传入。
// 预算文案固定不变（不随轮次更新），system prompt 跨轮保持一致 → 不破坏 LLM 前缀缓存；
// 子 agent 每轮都能看到预算，据此控制工作幅度，避免无限制扩张。
func buildSubAgentSystemPrompt(spec subagentSpec, maxIter, timeoutSecs int) string {
	var buf strings.Builder
	buf.WriteString("你是 xhanHelper 的子 agent。\n")
	buf.WriteString(fmt.Sprintf("当前时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
	buf.WriteString(spec.prompt)
	buf.WriteString("\n\n")
	buf.WriteString("任务预算（重要）：\n")
	buf.WriteString(fmt.Sprintf("- 你最多执行 %d 轮工具调用（第 %d 轮为强制总结轮，届时禁止调用工具）\n", maxIter-1, maxIter))
	buf.WriteString(fmt.Sprintf("- 总时长约 %d 秒\n", timeoutSecs))
	buf.WriteString("请据此控制工作幅度，在预算内一次性完成，不要无限制地扩张步骤。\n")
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
	// 步骤 0 — 类型查表分派（prompt/白名单/maxToolResult/客户端要求）
	spec, ok := subagentSpecs[opts.SubAgentType]
	if !ok {
		return &tool.SubAgentRunResult{
			Success: false,
			Error:   fmt.Sprintf("未知子 agent 类型: %s（可用：web_explore、file_explore）", opts.SubAgentType),
		}
	}

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

	// 步骤 3 — 客户端硬要求（web_explore/file_explore 都依赖 macOS 客户端，不降级）：
	//   - clientBridge 为 nil = 接线 bug（appinit 全局注入，prod 永不 nil）
	//   - 请求未携带客户端工具 = 客户端离线/Web 前端，直接报错，不再退化为无工具空转
	//   - 客户端在线但没提供所需工具组（如 file_explore 但客户端无 fs 工具）→ 同样报错
	// 全部放在创建子会话之前，避免错误路径留下孤儿子会话。
	agentClientTools := toClientTools(opts.ClientTools)
	clientBridge := o.clientBridge
	if clientBridge == nil {
		return &tool.SubAgentRunResult{
			Success: false,
			Error:   "客户端工具桥未就绪（接线错误），子任务无法执行。请稍后重试。",
		}
	}
	if spec.requiresClient && len(agentClientTools) == 0 {
		return &tool.SubAgentRunResult{
			Success: false,
			Error:   fmt.Sprintf("子任务 %s 需要 macOS 客户端在线（当前请求未携带客户端工具）。请先打开 Mac 客户端再发起该任务。", opts.SubAgentType),
		}
	}
	clientDefs := filterClientToolsByFuncWhitelist(buildClientToolDefs(agentClientTools, o.registry), spec.clientWhitelist)
	if spec.clientWhitelist != nil && len(clientDefs) == 0 {
		return &tool.SubAgentRunResult{
			Success: false,
			Error:   fmt.Sprintf("客户端未提供子任务 %s 所需的本地工具（%s），无法执行。", opts.SubAgentType, clientToolGroupNames(spec.clientWhitelist)),
		}
	}

	// 步骤 4 — 创建子 Conversation（列表不可见：ListByUser 已过滤 parent_id IS NULL）
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

	// 步骤 5 — 构建工具定义（服务端 + 客户端分别按白名单过滤，客户端按函数粒度）
	tools := o.registry.GetLLMToolsFiltered(spec.serverWhitelist)
	tools = append(tools, clientDefs...)
	// ask_user 注入：仅当客户端在线（ClientTools 非空）时——无浏览器场景 ask_user 无意义。
	// 注意：def 必须同时进入 tools（LLM 可见）与 ClientTools（MiniReAct clientIdx 分发索引），
	// 缺一不可——只加 def 会导致 LLM 能调用但分发路由不到客户端（"未找到工具 'ui'"）。
	tools, agentClientTools = appendAskUserTool(tools, agentClientTools)

	// 步骤 6 — 构造 MiniReAct（复用既有组件，含强制收敛轮/工具日志/事件回调）
	maxIter := o.subAgentMaxIterations
	if maxIter <= 0 {
		maxIter = 20 // 兜底防死循环（timeout 才是真实预算）
	}
	timeout := o.subAgentTimeout
	if timeout <= 0 {
		timeout = 600 * time.Second
	}
	react := NewMiniReAct(&MiniReActConfig{
		Client:        o.flashClient, // 子 agent 固定 Flash
		Tools:         tools,
		ToolRegistry:  o.registry,
		SystemPrompt:  buildSubAgentSystemPrompt(spec, maxIter, int(timeout.Seconds())),
		UserID:        userID,
		ConvID:        subConv.ID,
		MaxIterations: maxIter, // 主循环 maxIter-1 轮 + 1 轮强制收敛（MiniReAct 内置）
		MaxToolResult: spec.maxToolResult,
		Caller:        "subagent:" + opts.SubAgentType,
		SubAgent: SubAgentExt{
			ClientTools:       agentClientTools,
			ClientBridge:      clientBridge,
			ClientToolTimeout: o.clientToolTimeout,
			AllowedTools:      buildAllowedTools(spec),
			MsgSink:           &subAgentMsgSink{msgRepo: o.subMsgRepo, convID: subConv.ID, userID: userID},
		},
		OnEvent: o.makeSubAgentForwarder(opts.MainEvents, subConv.ID, opts.Description),
	})

	// 子任务活动面板：start 事件
	if opts.MainEvents != nil {
		o.sendEvent(opts.MainEvents, &llm.StreamEvent{Type: "subagent", Content: subAgentEventJSON(subConv.ID, opts.Description, "start", "", "started")})
	}

	// 步骤 8 — 超时控制与执行（成本中心：子循环 LLM 调用记 main_chat/sub_agent_run）
	// timeout 兜底已在步骤 6 归一（与 system prompt 预算文案同源）
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
