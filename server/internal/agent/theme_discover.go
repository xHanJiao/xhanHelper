package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

const themeDiscovererPrompt = `你是 xhanHelper 的「主题侦察员」。你的职责是：从用户最近的活动（聊天、笔记、记忆）中，发现用户生活里值得关注的方面，提名为主题。

一个「主题」是什么：不是一次性事件或待办，而是你对用户世界中某个方面的持续认知——一个值得你关注、记住、在未来对话中引用其状态的领域。每个主题回答的是：「这件事，用户现在的状态是什么？」

## 核心原则

1. 宁缺毋滥。拿不准就不提。返回空结果是正常行为。
2. 关注「用户生活中有意义的方面」——可以是习惯、关注点、决策、关系、项目等。一次性琐事（如扔垃圾、拿快递）不算。标注关注周期（duration）:
   - long: 年度关注（买房、职业规划）
   - medium: 月度关注（项目进展、学习课程）
   - short: 一两周关注（旅行计划、备考冲刺）
   - ephemeral: 几天关注（给朋友过生日、修水管）
3. 有理有据。每个提名必须附带理由（rationale）——从哪些数据中看出了什么信号。source_refs 必须是 get_topic_detail 返回的原文，不许编造。
4. **只输出 JSON，不要输出任何解释、分析过程或前言**。

## 你能使用的检索工具

1. get_topic_stats() — 获取 topic 聚类统计摘要
2. get_topic_detail(topic, topics) — 获取 topic 下所有 notes 和 memories 全文

## 检索策略

1. 先调 get_topic_stats 获取聚类后的 topic 分布统计
2. 对条目多、时间跨度长、tags 丰富的 cluster，调 get_topic_detail 获取全文
3. 对感兴趣的 cluster 调 get_topic_detail，不限次数，但要记得最多只能提名 2 个候选

## 判定标准：什么值得提名为主题

一个方向值得提名，需要满足：
- 是用户生活中一个有意义的方面（不是一次性琐事）
- 用户表现出主动关注（是用户发起的话题）
- 有一定可追踪性和变化空间
- 非 Agent 诱导的话题

## 判定标准：什么「不该」提名

以下情况一律不提名：
- 一次性琐事（扔垃圾、拿快递）
- 纯短期 todo（完成即结束）
- Agent 诱导的话题
- 太宽泛（如「生活」「工作」）
- 太窄（可追踪内容太少）
- 已有主题覆盖

## 输出格式

{
  "candidates": [
    {
      "name": "候选主题名，5 字以内",
      "description": "追踪什么，20 字以内",
      "rationale": "提名理由：从哪些数据中发现了什么信号，为什么值得关注。这也是主题的初始状态描述",
      "signal_strength": "high | medium",
      "duration": "long | medium | short | ephemeral",
      "source_refs": ["数据原文引用1", "数据原文引用2"]
    }
  ],
  "rejected": [
    {"direction": "曾考虑的方向", "reject_reason": "一次性琐事 / 太宽泛 / 太窄 / 已有主题覆盖 / 信号不足"}
  ]
}

如果没发现合适的候选，返回：
{"candidates": [], "rejected": [{"direction": "搜索概况", "reject_reason": "未发现值得提名的新主题"}]}`

// themeCandidate — LLM 产出的候选主题
type themeCandidate struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Rationale      string   `json:"rationale"`
	SignalStrength string   `json:"signal_strength"`
	Duration       string   `json:"duration"`
	SourceRefs     []string `json:"source_refs"`
}

// rejectedItem — 被拒绝的候选方向
type rejectedItem struct {
	Direction string `json:"direction"`
	Reason    string `json:"reject_reason"`
}

// llmDiscoverOutput — LLM 发现过程的输出
type llmDiscoverOutput struct {
	Candidates []themeCandidate `json:"candidates"`
	Rejected   []rejectedItem   `json:"rejected"`
}

// embedder 嵌入接口，*llm.Embedder 满足此接口
type embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

type ThemeDiscoverer struct {
	flashClient    llm.LLMClient
	retrievalTools map[string]tool.Tool
	themeStore     *ThemeStore
	embedder       embedder
	vectorStore    app.VectorStore
	themeEmbedder  *ThemeEmbedder
	conceptEmbed   *ConceptEmbedService
	traceRepo      *service.ReflectionTraceRepo
	dedupThreshold float64
	maxIterations  int
	modeHandlers   map[string]discoverFunc
}

func NewThemeDiscoverer(llmClients *app.LLMClients, infra *app.Infra, retrievalTools map[string]tool.Tool, themeStore *ThemeStore, themeEmbedder *ThemeEmbedder, conceptEmbed *ConceptEmbedService, traceRepo *service.ReflectionTraceRepo, maxIterations int, dedupThreshold float64) *ThemeDiscoverer {
	td := &ThemeDiscoverer{
		flashClient:    llmClients.Flash,
		retrievalTools: retrievalTools,
		themeStore:     themeStore,
		embedder:       infra.Embedder,
		vectorStore:    infra.VectorStore,
		themeEmbedder:  themeEmbedder,
		conceptEmbed:   conceptEmbed,
		traceRepo:      traceRepo,
		dedupThreshold: dedupThreshold,
		maxIterations:  maxIterations,
	}
	td.modeHandlers = map[string]discoverFunc{
		"conversation": td.discoverByConversation,
		"stats":        td.discoverByStats,
	}
	return td
}

type discoverFunc func(ctx context.Context, userID, themeList string, a discovererArgs) ([]themeCandidate, []rejectedItem, error)

func (td *ThemeDiscoverer) Name() string { return "theme_discoverer" }
func (td *ThemeDiscoverer) Description() string {
	return "从用户最近活动中发现值得关注的候选主题"
}

type discovererArgs struct {
	RecentMsgCount  int    `json:"recent_msg_count"`
	RecentNoteCount int    `json:"recent_note_count"`
	RecentMemCount  int    `json:"recent_mem_count"`
	Mode            string `json:"mode"`
	Messages        string `json:"messages"`
}

func (td *ThemeDiscoverer) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{
		Name:        "discover",
		Description: "扫描用户最近活动，发现值得提名的候选追踪主题",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"recent_msg_count":  map[string]interface{}{"type": "integer", "description": "最近7天新增消息数"},
				"recent_note_count": map[string]interface{}{"type": "integer", "description": "最近7天新增笔记数"},
				"recent_mem_count":  map[string]interface{}{"type": "integer", "description": "最近7天新增记忆数"},
				"mode":              map[string]interface{}{"type": "string", "description": "conversation 模式直接从传入的消息文本分析"},
				"messages":          map[string]interface{}{"type": "string", "description": "mode=conversation 时传入的消息文本"},
			},
		},
	}}
}

type DiscoverResult struct {
	Created  []createdTheme `json:"created"`
	Rejected []rejectedItem `json:"rejected"`
}

type createdTheme struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Execute 是主题发现的唯一入口：
//  1. LLM 发现 candidates
//  2. dedup — 向量去重 + 名称查重（多路）
//  3. 为通过去重的 candidate 创建 active 主题，用 rationale 写初始状态页

// ReadOnlyFuncs — 内部反思/分析工具，全函数只读（不参与主对话 plan 模式拦截）
func (td *ThemeDiscoverer) ReadOnlyFuncs() map[string]bool { return tool.AllReadOnly(td) }

func (td *ThemeDiscoverer) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	var a discovererArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &tool.Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}

	drafts, _ := td.themeStore.ListDrafts(ec.Ctx, ec.UserID)
	actives, _ := td.themeStore.ListActives(ec.Ctx, ec.UserID)
	allThemes := append(drafts, actives...)
	log.Printf("[theme_discoverer] existing themes: %d drafts + %d actives", len(drafts), len(actives))

	themeList := formatThemeList(allThemes)

	discoverMode := a.Mode
	if discoverMode == "" {
		discoverMode = "stats"
	}

	handler, ok := td.modeHandlers[discoverMode]
	if !ok {
		return &tool.Result{Success: false, Error: fmt.Sprintf("unknown mode: %s", discoverMode)}
	}

	// 步骤 1：LLM 发现候选
	candidates, rejected, err := handler(ec.Ctx, ec.UserID, themeList, a)
	if err != nil {
		log.Printf("[theme_discoverer] %s mode error: %v", discoverMode, err)
		return &tool.Result{Success: false, Error: fmt.Sprintf("发现失败: %v", err)}
	}

	// 步骤 2：多路去重（向量相似度 + 名称精确匹配）
	candidates, rejected = td.dedup(ec.Ctx, ec.UserID, candidates, rejected)

	// 步骤 3：为通过去重的 candidate 创建 active 主题
	result := DiscoverResult{Rejected: rejected}
	for _, c := range candidates {
		duration := c.Duration
		initialState := c.Rationale
		if initialState == "" {
			initialState = fmt.Sprintf("新主题：%s - %s", c.Name, c.Description)
		}

		metaJSON, _ := json.Marshal(map[string]interface{}{"candidate_info": c})
		metaStr := string(metaJSON)

		theme := &model.ThemeState{
			ID:          uuid.New().String(),
			UserID:      ec.UserID,
			Name:        c.Name,
			Description: c.Description,
			Status:      "active",
			Duration:    duration,
			StateText:   initialState,
			Metadata:    &metaStr,
		}
		if err := td.themeStore.Create(ec.Ctx, theme); err != nil {
			log.Printf("[theme] create error: %v", err)
			continue
		}
		log.Printf("[theme] active created: name=%s id=%s duration=%s", c.Name, theme.ID[:8], duration)

		if td.themeEmbedder != nil {
			go td.themeEmbedder.EnsureEmbedded(context.Background(), ec.UserID, theme.ID, c.Name, c.Description)
		}

		result.Created = append(result.Created, createdTheme{
			ID: theme.ID, Name: c.Name, Status: "active",
		})
	}

	data, _ := json.Marshal(result)
	log.Printf("[theme_discoverer] created=%d rejected=%d", len(result.Created), len(result.Rejected))
	return &tool.Result{Success: true, Data: data}
}

func formatThemeList(themes []ThemeStateInfo) string {
	var sb strings.Builder
	sb.WriteString("\n\n## 现有主题（含 draft + active，避免重复提名）\n")
	if len(themes) == 0 {
		sb.WriteString("（无）\n")
	} else {
		for _, t := range themes {
			extra := ""
			if t.Status == "active" {
				extra = fmt.Sprintf(" [status=active duration=%s]", t.Duration)
			} else {
				extra = " [status=draft]"
			}
			sb.WriteString(fmt.Sprintf("- %s：%s%s\n", t.Name, t.Description, extra))
		}
	}
	return sb.String()
}

func (td *ThemeDiscoverer) discoverByConversation(ctx context.Context, userID, themeList string, a discovererArgs) ([]themeCandidate, []rejectedItem, error) {
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryReflection,
		SubCategory: llmcost.SubThemeDiscoverConv,
		UserID:      userID,
		RefType:     "theme",
	})
	prompt := themeDiscovererPrompt + themeList + "\n请直接从以下对话消息中判断是否有值得提名的候选主题。不要调检索工具。\n"
	resp, err := td.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: prompt,
		Messages:     []llm.Message{{Role: "user", Content: a.Messages}},
		UserID:       userID,
	})
	if err != nil {
		return nil, nil, err
	}
	var output llmDiscoverOutput
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		log.Printf("[theme_discoverer] conversation parse error: %v", parseErr)
		return nil, nil, nil
	}
	return output.Candidates, output.Rejected, nil
}

func (td *ThemeDiscoverer) discoverByStats(ctx context.Context, userID, themeList string, a discovererArgs) ([]themeCandidate, []rejectedItem, error) {
	var sb strings.Builder
	sb.WriteString(themeList)
	sb.WriteString(fmt.Sprintf("\n## 最近 7 天活动概览\n"))
	sb.WriteString(fmt.Sprintf("- 新增消息：%d 条（含 user role）\n", a.RecentMsgCount))
	sb.WriteString(fmt.Sprintf("- 新增笔记：%d 条\n", a.RecentNoteCount))
	sb.WriteString(fmt.Sprintf("- 新增记忆：%d 条\n", a.RecentMemCount))
	sb.WriteString("\n请检索并判断是否有值得提名的候选主题。\n")

	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryReflection,
		SubCategory: llmcost.SubThemeDiscoverStats,
		UserID:      userID,
		RefType:     "theme",
	})
	loop := NewReactLoop(td.flashClient, td.retrievalTools, themeDiscovererPrompt, td.maxIterations, userID, "ThemeDiscoverer", td.traceRepo, CtxTraceID(ctx))
	result, err := loop.Run(ctx, sb.String())
	if err != nil {
		return nil, nil, err
	}
	var output llmDiscoverOutput
	if _, parseErr := ParseFlashJSON(result.FinalResponse, &output); parseErr != nil {
		return nil, nil, fmt.Errorf("parse: %w", parseErr)
	}
	return output.Candidates, output.Rejected, nil
}

// dedup 多路去重：名称精确匹配 + 向量语义相似度。
//
//   - 撞 active → reject
//   - 撞 closed / draft → reactivate 为 active
//   - 无匹配 → 保留，后续创建为新 active 主题
func (td *ThemeDiscoverer) dedup(ctx context.Context, userID string, candidates []themeCandidate, rejected []rejectedItem) ([]themeCandidate, []rejectedItem) {
	kept := make([]themeCandidate, 0, len(candidates))

	for _, c := range candidates {
		// 第一路：名称精确查重
		existing, _ := td.themeStore.GetByName(ctx, userID, c.Name)
		if existing != nil {
			if handled, reason := td.handleExisting(ctx, userID, existing); handled {
				rejected = append(rejected, rejectedItem{Direction: c.Name, Reason: reason})
				continue
			}
		}

		// 第二路：向量语义查重
		if handled, reason := td.handleVecSimilar(ctx, userID, c.Name, c.Description); handled {
			rejected = append(rejected, rejectedItem{Direction: c.Name, Reason: reason})
			continue
		}

		kept = append(kept, c)
	}
	return kept, rejected
}

// handleVecSimilar 向量查重：embed 候选名+描述，搜语义相似主题。
func (td *ThemeDiscoverer) handleVecSimilar(ctx context.Context, userID, name, desc string) (bool, string) {
	if td.embedder == nil || td.vectorStore == nil {
		return false, ""
	}
	vec, err := td.embedConcept(ctx, userID, name+" "+desc)
	if err != nil {
		log.Printf("[theme_discoverer] embed candidate %q error: %v", name, err)
		return false, ""
	}

	hits := td.vectorStore.Search(vec, 20, func(id string, _ []float64, metadata map[string]string) bool {
		return metadata["type"] == model.VectorTypeTheme && metadata["user_id"] == userID
	})

	// Phase 1: 收集需要查询 DB 的 theme ID
	var queryIDs []string
	hasActiveNil := false
	for _, hit := range hits {
		if hit.Score < td.dedupThreshold {
			continue
		}
		tid := hit.Metadata["theme_id"]
		if tid == "" {
			hasActiveNil = true
			break
		}
		queryIDs = append(queryIDs, tid)
	}

	if hasActiveNil {
		log.Printf("[theme_discoverer] candidate %q rejected: similar to active theme (legacy vector)", name)
		return true, fmt.Sprintf("向量去重：与已有 active 主题相似度 >= %.2f", td.dedupThreshold)
	}

	if len(queryIDs) == 0 {
		return false, ""
	}

	// Phase 2: 批量 DB 查询
	themeMap, err := td.themeStore.GetByIDs(ctx, queryIDs, userID)
	if err != nil {
		log.Printf("[theme_discoverer] GetByIDs error: %v", err)
		return false, ""
	}

	// Phase 3: 分拣 active 和 non-active
	hasActive := false
	var reactivateIDs []string
	for _, t := range themeMap {
		if t.Status == "active" {
			hasActive = true
			break
		}
		reactivateIDs = append(reactivateIDs, t.ID)
	}

	if hasActive {
		log.Printf("[theme_discoverer] candidate %q rejected: similar to active theme", name)
		return true, fmt.Sprintf("向量去重：与已有 active 主题相似度 >= %.2f", td.dedupThreshold)
	}

	if len(reactivateIDs) > 0 {
		for _, mid := range reactivateIDs {
			if err := td.themeStore.UpdateStatus(ctx, mid, userID, "active"); err != nil {
				log.Printf("[theme_discoverer] reactivate error: %v", err)
			} else {
				log.Printf("[theme_discoverer] reactivated theme %s to active", mid[:8])
			}
		}
		return true, "已重新激活语义相似的 closed/draft 主题"
	}

	return false, ""
}

// handleExisting 名称精确查重：按 candidate.name 查 DB，处理已有主题。
func (td *ThemeDiscoverer) handleExisting(ctx context.Context, userID string, t *ThemeStateInfo) (bool, string) {
	switch t.Status {
	case "active":
		log.Printf("[theme_discoverer] candidate rejected: name already active")
		return true, "已有同名 active 主题"
	case "closed", "draft":
		if err := td.themeStore.UpdateStatus(ctx, t.ID, userID, "active"); err != nil {
			log.Printf("[theme_discoverer] reactivate by name error: %v", err)
		} else {
			log.Printf("[theme_discoverer] %s theme %s reactivated by name to active", t.Status, t.ID[:8])
		}
		return true, fmt.Sprintf("已重新激活同名 %s 主题", t.Status)
	default:
		return false, ""
	}
}

// embedConcept 嵌入 concept 文本，优先复用缓存。
func (td *ThemeDiscoverer) embedConcept(ctx context.Context, userID, text string) ([]float64, error) {
	if td.conceptEmbed != nil {
		vec, err := td.conceptEmbed.GetOrEmbed(ctx, "topic", text, userID)
		if err == nil && len(vec) > 0 {
			return vec, nil
		}
	}
	return td.embedder.Embed(ctx, text)
}
