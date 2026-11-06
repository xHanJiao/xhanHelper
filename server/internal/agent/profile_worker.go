package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"

	"xhan-helper/internal/config"
	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/profilefield"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// 共享 System Prompt — 所有后处理 worker（summary/memory/state/profile 等）共用，
// 前缀固定以利用 LLM provider 的 prompt 缓存。每个 worker 的差异化内容全部放 user message。
const postChatSharedSystemPrompt = `你是一个对话后处理助手。负责对用户对话记录进行批量处理与分析。
输入的消息均为用户角色的发言，按时间顺序排列。`

// coldStartUserMessage — 冷启动 ReAct 的初始 user 消息（极简触发，具体指令在 system prompt 里）。
const coldStartUserMessage = "请搜索并提取我的用户画像。"

// coldStartMaxIterations — 冷启动 mini ReAct 最大轮次（含强制收敛轮，计入总数）。
const coldStartMaxIterations = 6

// coldStartFinalRoundPrompt — 冷启动强制收敛轮指令：主循环耗尽后逼出画像 JSON。
const coldStartFinalRoundPrompt = `[系统] 本轮已达最大步数限制。请不要再调用任何工具，直接根据你已收集到的信息输出用户画像 JSON。
按输出格式要求输出：{"字段名":["事实1","事实2"]}。没找到信息的字段省略；完全没找到输出 {}。`

// coldStartTools — 冷启动开放的只读工具（统一搜索覆盖 memory/note/summary/event/history 五个域）。
const coldStartTools = "unified_search"

// profileRules — 正常增量提取的规则（注入 user message XML <rules>）。
var profileRules = []string{
	"事实短句：无引导词（不写\"用户表示/他认为\"），直接写事实",
	"并列项用分号分隔；禁散文长句、禁重复",
	"已有事实未过时必须原样保留（复制原文）；已过时的省略",
	"若某字段所有事实均已过时，输出空数组表示清空该字段（如{\"长期目标\":[]}）",
	"只输出有变化或需修订的字段；无变化输出 {}",
	"只提取用户明确陈述的事实，不要从对话中推断",
	"无法确定的信息宁可省略，不要猜测",
}

// profileOutputFormat — 正常增量提取的输出格式约束（注入 user message XML <output_format>）。
const profileOutputFormat = `输出纯JSON（不要用markdown代码块包裹）：{"字段名":["事实1","事实2"],...}。
每个字段输出其完整事实列表（含保留的旧事实，原样复制原文）；仅输出本次有新信息或需修订的字段；无变化输出 {}。
单值字段输出长度为1的数组，如{"称呼":["哥"]}。`

// profileLegacyTask — 延迟迁移模式的 task 提示（facts 表为空、旧表有值时）。
const profileLegacyTask = "从以下对话记录中提取用户画像。注意：existing_profile 中的值是历史画像文本（可能混装多条事实），请拆分为独立事实列表后按格式输出"

// profileMigrationTask — 启动迁移的 task（纯拆分历史画像，无新对话）。
const profileMigrationTask = "请将以下历史画像文本拆分为独立事实列表"

// profileMigrationRules — 启动迁移专用规则（与增量不同：要求全量输出所有字段）。
var profileMigrationRules = []string{
	"对所有已有画像字段输出完整事实列表（拆分为独立事实）",
	"事实短句：无引导词，直接写事实；并列项用分号分隔；禁散文长句",
	"未过时的信息必须保留（可适当压缩原文）；不确定的信息保留原文",
	"单值字段输出长度为 1 的数组",
}

// profileMigrationOutputFormat — 启动迁移输出格式（全量，非"仅变化字段"）。
const profileMigrationOutputFormat = `输出纯JSON（不要用markdown代码块包裹）：{"字段名":["事实1","事实2"],...}。
对所有已有画像字段输出完整事实列表；单值字段输出长度为1的数组，如{"称呼":["哥"]}。
如果完全没有可拆分的信息，输出 {}。`

// quotaRetryMax — 配额校验失败后反馈 LLM 重新生成的最大次数。
const quotaRetryMax = 2

// cleanOutdatedKeepBatches — outdated 审计保留的批次数量，更旧的物理清理。
const cleanOutdatedKeepBatches = 3

// profilePlaceholders — LLM 可能输出的占位值，视为无信息，跳过写入。
var profilePlaceholders = map[string]bool{
	"": true, "未知": true, "无": true, "暂无": true, "不详": true,
}

// UserProfileWorker — 用户画像稳定层提取（PostChatWorker）。
// 双模式：
//   - 冷启动（facts 表为空且旧表为空）：mini ReAct（deep mode，Pro 模型，可选），LLM 用 unified_search 自主搜集信息后生成画像
//   - 正常增量（已有画像）：单次 Flash 提取当前 session user 消息中的新事实
//   - 延迟迁移（facts 表为空但旧表有值）：增量提取路径，existing 用旧表整段值，LLM 拆分后写入 facts 表
//
// 稳定层只走本 worker 单一写入通道，不向对话 LLM 提供画像修改工具。
type UserProfileWorker struct {
	flashClient  llm.LLMClient
	strongClient llm.LLMClient // 可选：冷启动用 Pro 模型（deep mode），nil 回退 Flash
	registry     *tool.Registry
	profileRepo  *service.UserProfileRepo // 旧表（延迟迁移读取）
	factRepo     *service.UserProfileFactRepo
	userRepo     *service.UserRepo // 启动迁移遍历用户
}

// UserProfileWorkerConfig — UserProfileWorker 依赖配置。
type UserProfileWorkerConfig struct {
	FlashClient  llm.LLMClient
	StrongClient llm.LLMClient // 可选：冷启动用 Pro 模型（deep mode），nil 回退 Flash
	Registry     *tool.Registry
	ProfileRepo  *service.UserProfileRepo
	FactRepo     *service.UserProfileFactRepo
	UserRepo     *service.UserRepo // 启动迁移遍历用户；nil 时 RunStartupMigration 直接返回
}

// NewUserProfileWorker 创建画像提取 worker。必需依赖为 nil 时 panic（接线 bug 暴露在明处）。
func NewUserProfileWorker(cfg *UserProfileWorkerConfig) *UserProfileWorker {
	if cfg.FlashClient == nil || cfg.Registry == nil || cfg.ProfileRepo == nil || cfg.FactRepo == nil {
		panic("profile worker: required dependency nil (wiring bug)")
	}
	return &UserProfileWorker{
		flashClient:  cfg.FlashClient,
		strongClient: cfg.StrongClient,
		registry:     cfg.Registry,
		profileRepo:  cfg.ProfileRepo,
		factRepo:     cfg.FactRepo,
		userRepo:     cfg.UserRepo,
	}
}

// Name 实现 PostChatWorker。
func (w *UserProfileWorker) Name() string { return "profile" }

// Process 实现 PostChatWorker。按画像是否存在分流冷启动 / 正常增量。
func (w *UserProfileWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubProfileExtract,
		UserID:      sess.UserID,
		ConvID:      sess.ConvID,
	})
	hasFacts, err := w.factRepo.HasActive(ctx, sess.UserID)
	if err != nil {
		return fmt.Errorf("profile: HasActive: %w", err)
	}
	if hasFacts {
		return w.extractNormal(ctx, sess, false)
	}
	hasLegacy, err := w.profileRepo.HasAny(ctx, sess.UserID)
	if err != nil {
		return fmt.Errorf("profile: HasAny(legacy): %w", err)
	}
	if !hasLegacy {
		log.Printf("[profile] cold start: user=%s conv=%s", sess.UserID, sess.ConvID[:min(8, len(sess.ConvID))])
		return w.runColdStartReAct(ctx, sess.UserID)
	}
	log.Printf("[profile] legacy migration: user=%s conv=%s", sess.UserID, sess.ConvID[:min(8, len(sess.ConvID))])
	return w.extractNormal(ctx, sess, true)
}

// ==================== 冷启动：mini ReAct ====================

// coldStartSystemPrompt 构建冷启动 system prompt（纯文本分节，含工具指引 + 字段定义 + 输出格式 + 规则）。
func coldStartSystemPrompt() string {
	return fmt.Sprintf(`你是一个用户画像提取助手。你的任务是搜索用户的记忆、笔记、
历史对话，了解用户的基本背景信息，然后提取用户画像。

—————— 可用工具 ——————

你只有一个工具：unified_search
- unified_search.semantic_search(query, types, top_k)
  语义搜索。types 可选 memory/note/summary/event/history，默认 memory/note/summary/event（不含 history；查历史需显式传 history）。top_k 默认 5
- unified_search.keyword_search(keywords, types, topic, top_k)
  关键词搜索。keywords 用空格/逗号分隔。types 同上（默认不含 history）；history 只搜最近 90 天，无命中放宽到 365 天。topic 仅对 memory/note 生效

建议先做一次宽泛搜索了解用户，再针对需要的字段做精确搜索。

—————— 提取字段 ——————

%s

—————— 输出格式 ——————

信息收集完成后，输出纯 JSON（不要用 markdown 代码块包裹）：
{"字段名":["事实1","事实2"],...}
每个字段输出完整事实列表；单值字段输出长度为1的数组，如{"称呼":["哥"]}。

没找到信息的字段直接省略，不要填"未知"、"无"等占位符。
如果完全没有找到任何信息，输出 {}。

—————— 重要规则 ——————

- 事实短句：无引导词，直接写事实；并列项分号分隔，禁散文长句
- 只填写用户明确陈述过的事实，禁止用你的推断填充
- 若某字段所有事实均已过时，输出空数组表示清空该字段（如{"长期目标":[]}）
- 不确定的信息宁可省略，不要猜测
- 你最多有 6 轮搜索机会，合理规划搜索内容`, profilefield.ProfileExtractFieldText())
}

// runColdStartReAct 冷启动：LLM 用 unified_search 自主搜集信息，输出画像 JSON 后写入 facts 表。
// 使用通用 MiniReAct 组件执行循环。
func (w *UserProfileWorker) runColdStartReAct(ctx context.Context, userID string) error {
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubProfileColdStart,
		UserID:      userID,
	})
	tools := w.registry.GetLLMToolsFiltered([]string{coldStartTools})

	// 冷启动用 deep mode（Pro 模型）提升提取质量；未注入 StrongClient 时回退 Flash
	client := w.flashClient
	var thinking *bool
	if w.strongClient != nil {
		client = w.strongClient
		t := true
		thinking = &t
	}

	react := NewMiniReAct(&MiniReActConfig{
		Client:           client,
		Tools:            tools,
		ToolRegistry:     w.registry,
		SystemPrompt:     coldStartSystemPrompt(),
		FinalRoundPrompt: coldStartFinalRoundPrompt,
		Thinking:         thinking,
		UserID:           userID,
		MaxIterations:    coldStartMaxIterations,
		Caller:           "profile-coldstart",
	})
	log.Printf("[profile] cold start: model=%s max_iter=%d", client.ModelName(), coldStartMaxIterations)
	result, err := react.Run(ctx, coldStartUserMessage)
	if err != nil {
		return fmt.Errorf("profile: cold start: %w", err)
	}
	if result.TerminatedEarly || result.FinalContent == "" {
		log.Printf("[profile] cold start: ended without final JSON (iterations=%d), skip", result.Iterations)
		return nil
	}

	// 无工具调用 → 解析最终画像 JSON
	extracted, err := parseExtractedFacts(result.FinalContent)
	if err != nil {
		log.Printf("[profile] cold start: final JSON parse error: %v, raw=%s", err, truncateStr(result.FinalContent, 200))
		return nil // 不重试，下次 session_end 自然重跑
	}
	log.Printf("[profile] cold start: extracted %d fields, iterations=%d", len(extracted), result.Iterations)
	w.applyExtracted(ctx, userID, extracted)
	return nil
}

// ==================== 正常增量提取 ====================

// extractNormal 正常增量：加载已有画像 + session user 消息编码为 XML，单次 Flash 提取新事实。
// legacy=true 时（facts 空 + 旧表有值）走延迟迁移：existing 用旧表整段值，LLM 拆分为事实列表。
// 配额校验失败时反馈 LLM 重新生成（最多 quotaRetryMax 次），仍失败则截断兜底。
func (w *UserProfileWorker) extractNormal(ctx context.Context, sess *SessionSnapshot, legacy bool) error {
	if len(sess.GetUserMessages()) == 0 {
		return nil
	}

	userMsg, err := w.buildExtractionXML(ctx, sess, legacy)
	if err != nil {
		return err
	}

	extracted, err := w.chatExtractWithQuotaRetry(ctx, sess.UserID, userMsg)
	if err != nil {
		return err
	}
	if len(extracted) == 0 {
		return nil
	}
	log.Printf("[profile] normal: extracted %d fields, conv=%s", len(extracted), sess.ConvID[:min(8, len(sess.ConvID))])
	w.applyExtracted(ctx, sess.UserID, extracted)
	return nil
}

// chatExtractWithQuotaRetry 公共提取调用：ChatSync → parse → 配额校验，违规反馈 LLM 重新生成
// （最多 quotaRetryMax 次）；重试耗尽仍超限 → 截断兜底（返回 parsed，调用方 applyExtracted 时
// filterFacts 截断）。extractNormal 与 RunStartupMigration 共用。
func (w *UserProfileWorker) chatExtractWithQuotaRetry(ctx context.Context, userID, userMsg string) (map[string][]string, error) {
	messages := []llm.Message{{Role: "user", Content: userMsg}}
	var extracted map[string][]string
	for attempt := 0; ; attempt++ {
		resp, err := w.flashClient.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: postChatSharedSystemPrompt,
			Messages:     messages,
			UserID:       userID,
		})
		if err != nil {
			return nil, fmt.Errorf("profile: extract chat: %w", err)
		}
		parsed, err := parseExtractedFacts(resp.Content)
		if err != nil {
			log.Printf("[profile] extract: JSON parse error: %v, raw=%s", err, truncateStr(resp.Content, 200))
			return nil, nil
		}
		violations := w.validateQuota(ctx, userID, parsed)
		if len(violations) == 0 {
			extracted = parsed
			break
		}
		if attempt >= quotaRetryMax {
			// 重试仍超限（极罕见）→ 截断兜底 + log，保留信息主体
			log.Printf("[profile] extract: quota retries exhausted (%d), truncate fallback: %s",
				quotaRetryMax, strings.Join(violations, "; "))
			return parsed, nil
		}
		log.Printf("[profile] extract: quota violation (attempt %d): %s", attempt+1, strings.Join(violations, "; "))
		messages = append(messages,
			llm.Message{Role: "assistant", Content: resp.Content},
			llm.Message{Role: "user", Content: buildQuotaFeedback(violations)},
		)
	}
	return extracted, nil
}

// ==================== 解析与写入 ====================

// parseExtractedFacts 解析 LLM 输出 JSON。兼容字符串单值（"字段":"值"）与数组（"字段":["f1","f2"]）。
func parseExtractedFacts(content string) (map[string][]string, error) {
	var raw map[string]json.RawMessage
	if _, err := ParseFlashJSON(content, &raw); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(raw))
	for field, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out[field] = []string{s}
			continue
		}
		var arr []string
		if err := json.Unmarshal(r, &arr); err != nil {
			log.Printf("[profile] field %q value not string or []string: %s", field, truncateStr(string(r), 80))
			continue
		}
		out[field] = arr
	}
	return out, nil
}

// quotaViolation — 配额校验违规项（文案直接给 LLM 看）。
type quotaViolation struct {
	Field string
	Msg   string
}

// validateQuota 校验提取结果的配额（MaxLen 单条长度 / MaxFacts 条数）。
// 只校验配额，不校验白名单（白名单外的字段在 applyExtracted 静默丢弃，不浪费重生成）。
// 返回违规列表，空 = 合规。
func (w *UserProfileWorker) validateQuota(_ context.Context, _ string, extracted map[string][]string) []string {
	var violations []string
	for field, facts := range extracted {
		def := profilefield.ProfileFieldDesc(field)
		if def == nil {
			continue
		}
		for i, f := range facts {
			if utf8.RuneCountInString(f) > def.MaxLen {
				violations = append(violations, fmt.Sprintf("字段\"%s\"第 %d 条 %d 字，超过上限 %d 字", field, i+1, utf8.RuneCountInString(f), def.MaxLen))
			}
		}
		if len(facts) > def.MaxFacts {
			violations = append(violations, fmt.Sprintf("字段\"%s\"共 %d 条，超过上限 %d 条", field, len(facts), def.MaxFacts))
		}
	}
	return violations
}

// buildQuotaFeedback 把违规项拼成给 LLM 的反馈（要求重新输出）。
func buildQuotaFeedback(violations []string) string {
	return "以下输出不满足约束：\n" + strings.Join(violations, "\n") + "\n请精简/拆分/删除违规内容后，按原格式重新输出完整 JSON（不要用 markdown 代码块包裹）。"
}

// applyExtracted 按白名单过滤并写入 facts 表。
// 每字段：与现有 active facts 精确比较（排序后 join），相同则跳过（防膨胀第一道闸）；
// 不同则全量替换（旧 active 标 outdated + 新 facts 插入）。
// 超限兜底：单条 rune 截断、条数截取前 N（重试耗尽后的降级路径）。
func (w *UserProfileWorker) applyExtracted(ctx context.Context, userID string, extracted map[string][]string) {
	batchID := idgen.Next()
	existing, err := w.factRepo.GetActiveByUser(ctx, userID)
	if err != nil {
		log.Printf("[profile] GetActiveByUser error: %v", err)
		return
	}
	activeByField := make(map[string][]string, 8)
	for _, f := range existing {
		activeByField[f.Field] = append(activeByField[f.Field], f.Value)
	}

	for field, facts := range extracted {
		if !profilefield.InProfileWhitelist(field) {
			log.Printf("[profile] field %q not in whitelist, drop", field)
			continue
		}
		def := profilefield.ProfileFieldDesc(field)
		// 空数组 = 清空意图（LLM 判定该字段所有事实均已过时）
		if len(facts) == 0 {
			if len(activeByField[field]) == 0 {
				continue
			}
			if err := w.factRepo.ReplaceFieldFacts(ctx, userID, field, nil, batchID); err != nil {
				log.Printf("[profile] ReplaceFieldFacts field %s error: %v", field, err)
				continue
			}
			log.Printf("[profile] cleared field %s", field)
			continue
		}
		facts = filterFacts(facts, def)
		if len(facts) == 0 {
			continue // 过滤后变空（全是占位符）→ 忽略，不当作清空
		}
		if factsEqual(facts, activeByField[field]) {
			continue // 无变化，跳过（不产生新批次）
		}
		if err := w.factRepo.ReplaceFieldFacts(ctx, userID, field, facts, batchID); err != nil {
			log.Printf("[profile] ReplaceFieldFacts field %s error: %v", field, err)
			continue
		}
		log.Printf("[profile] replaced field %s: %d facts", field, len(facts))
	}

	// outdated 审计保留最近 N 批，更旧物理清理
	if n, err := w.factRepo.CleanupOutdated(ctx, userID, cleanOutdatedKeepBatches); err != nil {
		log.Printf("[profile] CleanupOutdated error: %v", err)
	} else if n > 0 {
		log.Printf("[profile] cleaned %d outdated facts (user=%s)", n, userID)
	}
}

// filterFacts 过滤占位值 + 超限兜底截断（按 MaxLen 截单条、MaxFacts 截条数）。
func filterFacts(facts []string, def *profilefield.ProfileFieldDef) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		f = strings.TrimSpace(f)
		if profilePlaceholders[f] {
			continue
		}
		if utf8.RuneCountInString(f) > def.MaxLen {
			log.Printf("[profile] fact too long (field=%s, %d>%d), truncate: %s",
				def.Field, utf8.RuneCountInString(f), def.MaxLen, truncateStr(f, 60))
			f = truncateRunes(f, def.MaxLen)
		}
		out = append(out, f)
	}
	if len(out) > def.MaxFacts {
		log.Printf("[profile] too many facts (field=%s, %d>%d), keep first %d",
			def.Field, len(out), def.MaxFacts, def.MaxFacts)
		out = out[:def.MaxFacts]
	}
	return out
}

// factsEqual 判断两个事实集是否等价：排序后拼接比较（顺序无关，内容精确）。
func factsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]string(nil), a...)
	sb := append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	return strings.Join(sa, "\x00") == strings.Join(sb, "\x00")
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// ==================== 启动迁移 ====================

// RunStartupMigration 启动时异步迁移旧画像：对 facts 表为空但旧表有数据的用户，
// 用专用 prompt 全量拆分旧值写入 facts 表。幂等（HasActive 已迁则跳过），
// 失败只打日志不阻塞（下次启动或 session_end legacy 兜底重试）。
// 单用户场景配置开关控制，迁移完成后可关闭。
func (w *UserProfileWorker) RunStartupMigration(ctx context.Context) {
	if w.userRepo == nil {
		log.Printf("[profile] startup migration: userRepo nil, skip")
		return
	}
	users, err := w.userRepo.ListAll(ctx)
	if err != nil {
		log.Printf("[profile] startup migration: ListAll error: %v", err)
		return
	}
	for _, u := range users {
		hasFacts, err := w.factRepo.HasActive(ctx, u.ID)
		if err != nil {
			log.Printf("[profile] startup migration: HasActive error: %v", err)
			continue
		}
		if hasFacts {
			continue // 已迁移（或已有增量数据），跳过
		}
		legacy, err := w.profileRepo.GetByUser(ctx, u.ID)
		if err != nil {
			log.Printf("[profile] startup migration: GetByUser error: %v", err)
			continue
		}
		if len(legacy) == 0 {
			continue
		}
		log.Printf("[profile] startup migration: user=%s legacy_fields=%d", u.ID[:min(8, len(u.ID))], len(legacy))
		ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
			Category:    llmcost.CategoryPostChat,
			SubCategory: llmcost.SubProfileExtract,
			UserID:      u.ID,
		})
		userMsg, err := w.buildMigrationXML(ctx, legacy)
		if err != nil {
			log.Printf("[profile] startup migration: build XML error: %v", err)
			continue
		}
		extracted, err := w.chatExtractWithQuotaRetry(ctx, u.ID, userMsg)
		if err != nil {
			log.Printf("[profile] startup migration: chat error: %v", err)
			continue
		}
		if len(extracted) == 0 {
			continue
		}
		log.Printf("[profile] startup migration: user=%s extracted %d fields", u.ID[:min(8, len(u.ID))], len(extracted))
		w.applyExtracted(ctx, u.ID, extracted)
	}
	log.Printf("[profile] startup migration: done")
}

// buildMigrationXML 构造启动迁移的 user message XML（existing = 旧表全部字段，无 messages）。
func (w *UserProfileWorker) buildMigrationXML(ctx context.Context, legacy []model.UserProfile) (string, error) {
	doc := profileExtractionXML{
		Task:         profileMigrationTask,
		Fields:       profilefield.ProfileExtractFieldTextBrief(),
		Rules:        profileMigrationRules,
		OutputFormat: profileMigrationOutputFormat,
	}
	if len(legacy) > 0 {
		ex := &existingProfileXML{Fields: make([]existingFieldXML, 0, len(legacy))}
		for _, p := range legacy {
			ex.Fields = append(ex.Fields, existingFieldXML{Key: p.Field, Value: p.FieldValue})
		}
		doc.ExistingProfile = ex
	}
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return "", fmt.Errorf("profile: encode migration XML: %w", err)
	}
	return buf.String(), nil
}

// ==================== XML 编码 ====================

// profileExtractionXML — 正常增量 user message 的 XML 结构。
type profileExtractionXML struct {
	XMLName         struct{}            `xml:"profile_extraction"`
	Task            string              `xml:"task"`
	ExistingProfile *existingProfileXML `xml:"existing_profile,omitempty"`
	Fields          string              `xml:"fields"`
	Messages        []profileMsgXML     `xml:"messages>msg"`
	Rules           []string            `xml:"rules>rule"`
	OutputFormat    string              `xml:"output_format"`
}

type existingProfileXML struct {
	Fields []existingFieldXML `xml:"field"`
}

type existingFieldXML struct {
	Key   string            `xml:"key,attr"`
	Facts []existingFactXML `xml:"fact"`
	Value string            `xml:"chardata,omitempty"` // legacy 模式（旧表整段值）
}

type existingFactXML struct {
	CreatedAt string `xml:"created_at,attr,omitempty"`
	Text      string `xml:",chardata"`
}

type profileMsgXML struct {
	Time string `xml:"time,attr"`
	Text string `xml:",chardata"`
}

// buildExtractionXML 构建增量提取的 user message XML。
// 优先用 facts 表 active 事实；legacy=true 时用旧表整段值（延迟迁移拆分）。
func (w *UserProfileWorker) buildExtractionXML(ctx context.Context, sess *SessionSnapshot, legacy bool) (string, error) {
	doc := profileExtractionXML{
		Fields:       profilefield.ProfileExtractFieldTextBrief(),
		Messages:     buildProfileMsgs(sess.GetUserMessages()),
		Rules:        profileRules,
		OutputFormat: profileOutputFormat,
	}

	if legacy {
		doc.Task = profileLegacyTask
		existing, err := w.profileRepo.GetByUser(ctx, sess.UserID)
		if err != nil {
			return "", fmt.Errorf("profile: GetByUser(legacy): %w", err)
		}
		if len(existing) > 0 {
			ex := &existingProfileXML{Fields: make([]existingFieldXML, 0, len(existing))}
			for _, p := range existing {
				ex.Fields = append(ex.Fields, existingFieldXML{
					Key:   p.Field,
					Value: p.FieldValue,
				})
			}
			doc.ExistingProfile = ex
		}
	} else {
		doc.Task = "从以下对话记录中提取用户新画像信息"
		existing, err := w.factRepo.GetActiveByUser(ctx, sess.UserID)
		if err != nil {
			return "", fmt.Errorf("profile: GetActiveByUser: %w", err)
		}
		if len(existing) > 0 {
			byField := make(map[string][]existingFactXML)
			for _, f := range existing {
				byField[f.Field] = append(byField[f.Field], existingFactXML{
					CreatedAt: f.CreatedAt.In(config.BJ).Format("2006-01-02"),
					Text:      f.Value,
				})
			}
			ex := &existingProfileXML{Fields: make([]existingFieldXML, 0, len(byField))}
			for field, facts := range byField {
				ex.Fields = append(ex.Fields, existingFieldXML{Key: field, Facts: facts})
			}
			doc.ExistingProfile = ex
		}
	}

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return "", fmt.Errorf("profile: encode extraction XML: %w", err)
	}
	return buf.String(), nil
}

// buildProfileMsgs 把 session user 消息编码为 <messages> 块。
func buildProfileMsgs(msgs []model.Message) []profileMsgXML {
	out := make([]profileMsgXML, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, profileMsgXML{
			Time: m.CreatedAt.In(config.BJ).Format("01-02 15:04"),
			Text: m.Content,
		})
	}
	return out
}

// truncateStr 截断长字符串用于日志。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
