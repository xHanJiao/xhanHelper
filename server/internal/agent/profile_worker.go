package agent

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"strings"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
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
const coldStartMaxIterations = 10

// coldStartFinalRoundPrompt — 冷启动强制收敛轮指令：主循环耗尽后逼出画像 JSON。
const coldStartFinalRoundPrompt = `[系统] 本轮已达最大步数限制。请不要再调用任何工具，直接根据你已收集到的信息输出用户画像 JSON。
按输出格式要求输出：{"字段名1":"值1","字段名2":"值2"}。没找到信息的字段省略；完全没找到输出 {}。`

// coldStartTools — 冷启动开放的只读工具（统一搜索覆盖 memory/note/summary/theme/history 五个域）。
const coldStartTools = "unified_search"

// profileRules — 正常增量提取的规则（注入 user message XML <rules>）。
var profileRules = []string{
	"只提取用户明确陈述的事实，不要从对话中推断",
	"值要简洁，一句话能说清的事不要展开",
	"与已有画像一致的信息不要重复输出",
	"无法确定的信息宁可省略，不要猜测",
}

// profileOutputFormat — 正常增量提取的输出格式约束（注入 user message XML <output_format>）。
const profileOutputFormat = "输出纯JSON（不要用markdown代码块包裹），仅包含本次对话中新出现或变更的字段：{\"字段名\":\"新值\"}。如果本次对话中没有发现任何新画像信息，输出 {}"

// profilePlaceholders — LLM 可能输出的占位值，视为无信息，跳过写入。
var profilePlaceholders = map[string]bool{
	"": true, "未知": true, "无": true, "暂无": true, "不详": true,
}

// UserProfileWorker — 用户画像稳定层提取（PostChatWorker）。
// 双模式：
//   - 冷启动（画像表为空）：mini ReAct（deep mode，Pro 模型，可选），LLM 用 unified_search 自主搜集信息后生成画像
//   - 正常增量（已有画像）：单次 Flash 提取当前 session user 消息中的新事实
//
// 稳定层只走本 worker 单一写入通道，不向对话 LLM 提供画像修改工具。
type UserProfileWorker struct {
	flashClient  llm.LLMClient
	strongClient llm.LLMClient // 可选：冷启动用 Pro 模型（deep mode），nil 回退 Flash
	registry     *tool.Registry
	profileRepo  *service.UserProfileRepo
}

// UserProfileWorkerConfig — UserProfileWorker 依赖配置。
type UserProfileWorkerConfig struct {
	FlashClient  llm.LLMClient
	StrongClient llm.LLMClient // 可选：冷启动用 Pro 模型（deep mode），nil 回退 Flash
	Registry     *tool.Registry
	ProfileRepo  *service.UserProfileRepo
}

// NewUserProfileWorker 创建画像提取 worker。必需依赖为 nil 时 panic（接线 bug 暴露在明处）。
func NewUserProfileWorker(cfg *UserProfileWorkerConfig) *UserProfileWorker {
	if cfg.FlashClient == nil || cfg.Registry == nil || cfg.ProfileRepo == nil {
		panic("profile worker: required dependency nil (wiring bug)")
	}
	return &UserProfileWorker{
		flashClient:  cfg.FlashClient,
		strongClient: cfg.StrongClient,
		registry:     cfg.Registry,
		profileRepo:  cfg.ProfileRepo,
	}
}

// Name 实现 PostChatWorker。
func (w *UserProfileWorker) Name() string { return "profile" }

// Process 实现 PostChatWorker。按画像是否存在分流冷启动 / 正常增量。
func (w *UserProfileWorker) Process(ctx context.Context, sess *SessionSnapshot) error {
	hasAny, err := w.profileRepo.HasAny(ctx, sess.UserID)
	if err != nil {
		return fmt.Errorf("profile: HasAny: %w", err)
	}
	if !hasAny {
		log.Printf("[profile] cold start: user=%s conv=%s", sess.UserID, sess.ConvID[:min(8, len(sess.ConvID))])
		return w.runColdStartReAct(ctx, sess.UserID)
	}
	return w.extractNormal(ctx, sess)
}

// ==================== 冷启动：mini ReAct ====================

// coldStartSystemPrompt 构建冷启动 system prompt（纯文本分节，含工具指引 + 字段定义 + 输出格式 + 规则）。
func coldStartSystemPrompt() string {
	return fmt.Sprintf(`你是一个用户画像提取助手。你的任务是搜索用户的记忆、笔记、
历史对话，了解用户的基本背景信息，然后提取用户画像。

—————— 可用工具 ——————

你只有一个工具：unified_search
- unified_search.semantic_search(query, types, top_k)
  语义搜索。types 可选 memory/note/summary/theme/history，默认全部。top_k 默认 5
- unified_search.keyword_search(keywords, types, topic, top_k)
  关键词搜索。keywords 用空格/逗号分隔。types 同上，topic 仅对 memory/note 生效

建议先做一次宽泛搜索了解用户，再针对需要的字段做精确搜索。

—————— 提取字段 ——————

%s

—————— 输出格式 ——————

信息收集完成后，输出纯 JSON（不要用 markdown 代码块包裹）：
{"字段名1":"值1","字段名2":"值2"}

没找到信息的字段直接省略，不要填"未知"、"无"等占位符。
如果完全没有找到任何信息，输出 {}。

—————— 重要规则 ——————

- 只填写用户明确陈述过的事实，禁止用你的推断填充
- 值要简洁，一句话能说清
- 不确定的信息宁可省略，不要猜测
- 你最多有 9 轮搜索机会，合理规划搜索内容`, profilefield.ProfileExtractFieldText())
}

// runColdStartReAct 冷启动：LLM 用 unified_search 自主搜集信息，输出画像 JSON 后写入 DB。
// 使用通用 MiniReAct 组件执行循环。
func (w *UserProfileWorker) runColdStartReAct(ctx context.Context, userID string) error {
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
	var extracted map[string]string
	if _, err := ParseFlashJSON(result.FinalContent, &extracted); err != nil {
		log.Printf("[profile] cold start: final JSON parse error: %v, raw=%s", err, truncateStr(result.FinalContent, 200))
		return nil // 不重试，下次 session_end 自然重跑
	}
	log.Printf("[profile] cold start: extracted %d fields, iterations=%d", len(extracted), result.Iterations)
	w.upsertExtracted(ctx, userID, extracted)
	return nil
}

// ==================== 正常增量提取 ====================

// extractNormal 正常增量：加载已有画像 + session user 消息编码为 XML，单次 Flash 提取新事实。
func (w *UserProfileWorker) extractNormal(ctx context.Context, sess *SessionSnapshot) error {
	existing, err := w.profileRepo.GetByUser(ctx, sess.UserID)
	if err != nil {
		return fmt.Errorf("profile: GetByUser: %w", err)
	}
	if len(sess.Messages) == 0 {
		return nil
	}

	userMsg := buildProfileExtractionXML(existing, sess.Messages)
	resp, err := w.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: postChatSharedSystemPrompt,
		Messages:     []llm.Message{{Role: "user", Content: userMsg}},
		UserID:       sess.UserID,
	})
	if err != nil {
		return fmt.Errorf("profile: normal chat: %w", err)
	}

	var extracted map[string]string
	if _, err := ParseFlashJSON(resp.Content, &extracted); err != nil {
		log.Printf("[profile] normal: JSON parse error: %v, raw=%s", err, truncateStr(resp.Content, 200))
		return nil
	}
	if len(extracted) == 0 {
		return nil
	}
	log.Printf("[profile] normal: extracted %d fields, conv=%s", len(extracted), sess.ConvID[:min(8, len(sess.ConvID))])
	w.upsertExtracted(ctx, sess.UserID, extracted)
	return nil
}

// ==================== 写入 ====================

// upsertExtracted 按白名单过滤并写入画像。补充说明字段 source=llm_additional，其余 source=explicit。
func (w *UserProfileWorker) upsertExtracted(ctx context.Context, userID string, extracted map[string]string) {
	for field, value := range extracted {
		if !profilefield.InProfileWhitelist(field) {
			log.Printf("[profile] field %q not in whitelist, drop", field)
			continue
		}
		value = strings.TrimSpace(value)
		if profilePlaceholders[value] {
			continue
		}
		source := model.ProfileSourceExplicit
		if field == "补充说明" {
			source = model.ProfileSourceLLMAdditional
		}
		p := &model.UserProfile{
			UserID:     userID,
			Field:      field,
			FieldValue: value,
			Source:     source,
		}
		if err := w.profileRepo.Upsert(ctx, p); err != nil {
			log.Printf("[profile] upsert field %s error: %v", field, err)
		}
	}
}

// ==================== XML 编码 ====================

// profileExtractionXML — 正常增量 user message 的 XML 结构。
type profileExtractionXML struct {
	XMLName         struct{}             `xml:"profile_extraction"`
	Task            string               `xml:"task"`
	ExistingProfile *existingProfileXML  `xml:"existing_profile,omitempty"`
	Fields          []profileFieldDefXML `xml:"fields>field"`
	Messages        []profileMsgXML      `xml:"messages>msg"`
	Rules           []string             `xml:"rules>rule"`
	OutputFormat    string               `xml:"output_format"`
}

type existingProfileXML struct {
	Fields []existingFieldXML `xml:"field"`
}

type existingFieldXML struct {
	Key       string `xml:"key,attr"`
	Value     string `xml:",chardata"`
	UpdatedAt string `xml:"updated_at,attr,omitempty"`
}

type profileFieldDefXML struct {
	Name        string `xml:"name,attr"`
	Description string `xml:"description"`
}

type profileMsgXML struct {
	Time string `xml:"time,attr"`
	Text string `xml:",chardata"`
}

// buildProfileExtractionXML 构建正常增量提取的 user message XML。
func buildProfileExtractionXML(existing []model.UserProfile, msgs []model.Message) string {
	doc := profileExtractionXML{
		Task: "从以下对话记录中提取用户新画像信息",
		Fields: func() []profileFieldDefXML {
			fields := make([]profileFieldDefXML, 0, len(profilefield.ProfileFields))
			for _, f := range profilefield.ProfileFields {
				fields = append(fields, profileFieldDefXML{Name: f.Field, Description: f.ExtractDesc})
			}
			return fields
		}(),
		Messages: func() []profileMsgXML {
			out := make([]profileMsgXML, 0, len(msgs))
			for _, m := range msgs {
				out = append(out, profileMsgXML{
					Time: m.CreatedAt.In(config.BJ).Format("01-02 15:04"),
					Text: m.Content,
				})
			}
			return out
		}(),
		Rules:        profileRules,
		OutputFormat: profileOutputFormat,
	}
	if len(existing) > 0 {
		ex := &existingProfileXML{Fields: make([]existingFieldXML, 0, len(existing))}
		for _, p := range existing {
			ex.Fields = append(ex.Fields, existingFieldXML{
				Key:       p.Field,
				Value:     p.FieldValue,
				UpdatedAt: p.UpdatedAt.In(config.BJ).Format("2006-01-02"),
			})
		}
		doc.ExistingProfile = ex
	}

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		log.Printf("[profile] encode extraction XML error: %v", err)
		return ""
	}
	return buf.String()
}

// truncateStr 截断长字符串用于日志。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
