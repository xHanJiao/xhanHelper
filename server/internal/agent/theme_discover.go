package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

const themeDiscovererPrompt = `你是 xhanHelper 的「主题侦察员」。你的职责是：从用户最近的活动（聊天、笔记、记忆）中，发现对用户有价值的领域，提名为候选主题。

## 核心原则

1. 宁缺毋滥。一次最多提名 1 个候选。拿不准就不提。返回空结果是正常行为。
2. 关注「长期追踪价值」，不是「一次性好奇」。每个提名需要标注关注周期（duration）:
   - long: 年度关注（买房、职业规划）
   - medium: 月度关注（项目进展、学习课程）
   - short: 一两周关注（旅行计划、备考冲刺）
   - ephemeral: 几天关注（给朋友过生日、修水管）
3. 有理有据。每个提名必须附带理由——从哪些数据中看出了什么信号。source_refs 必须是 get_topic_detail 返回的原文，不许编造。
4. **只输出 JSON，不要输出任何解释、分析过程或前言**。

## 你能使用的检索工具

1. get_topic_stats() — 获取 topic 聚类统计摘要
2. get_topic_detail(topic, topics) — 获取 topic 下所有 notes 和 memories 全文

## 检索策略

1. 先调 get_topic_stats 获取聚类后的 topic 分布统计
2. 对条目多、时间跨度长、tags 丰富的 cluster，调 get_topic_detail 获取全文
3. 对感兴趣的 cluster 调 get_topic_detail，不限次数，但要记得最多只能提名 2 个候选

## 判定标准：什么值得提名为主题

一个方向值得提名，需要同时满足：
- 重复出现：过去 7-14 天内至少出现了 2-3 次相关讨论或记录
- 用户主动表达关注：是用户发起的话题
- 可追踪：这个主题有持续变化的空间
- 非短期任务：不是完成即结束的 todo

## 判定标准：什么「不该」提名

以下情况一律不提名：
- 一次性闲聊
- 短期 todo（完成即结束）
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
      "rationale": "提名理由：从哪些数据中发现了什么信号",
      "signal_strength": "high | medium",
      "duration": "long | medium | short | ephemeral",
      "source_refs": ["数据原文引用1", "数据原文引用2"]
    }
  ],
  "rejected": [
    {"direction": "曾考虑的方向", "reject_reason": "一次性闲聊 / 短期 todo / 太宽泛 / 太窄 / 已有主题覆盖 / 信号不足"}
  ]
}

如果没发现合适的候选，返回：
{"candidates": [], "rejected": [{"direction": "搜索概况", "reject_reason": "最近7天数据无重复出现的关注方向"}]}`

type ThemeDiscoverer struct {
	flashClient    llm.LLMClient
	retrievalTools map[string]tool.Tool
	themeStore     *ThemeStore
	embedder       *llm.Embedder
	vectorStore    VectorStorer
	themeEmbedder  *ThemeEmbedder
	dedupThreshold float64
	maxIterations  int
	modeHandlers   map[string]discoverFunc

	nominateCountThreshold int
	spanDaysThreshold      int
}

func NewThemeDiscoverer(flashClient llm.LLMClient, retrievalTools map[string]tool.Tool, themeStore *ThemeStore, embedder *llm.Embedder, vectorStore VectorStorer, themeEmbedder *ThemeEmbedder, maxIterations int, dedupThreshold float64) *ThemeDiscoverer {
	td := &ThemeDiscoverer{
		flashClient:            flashClient,
		retrievalTools:         retrievalTools,
		themeStore:             themeStore,
		embedder:               embedder,
		vectorStore:            vectorStore,
		themeEmbedder:          themeEmbedder,
		dedupThreshold:         dedupThreshold,
		maxIterations:          maxIterations,
		nominateCountThreshold: 3,
		spanDaysThreshold:      3,
	}
	td.modeHandlers = map[string]discoverFunc{
		"conversation": td.discoverByConversation,
		"stats":        td.discoverByStats,
	}
	return td
}

func (td *ThemeDiscoverer) SetUpgradeThresholds(nominateCount, spanDays int) {
	if nominateCount > 0 {
		td.nominateCountThreshold = nominateCount
	}
	if spanDays > 0 {
		td.spanDaysThreshold = spanDays
	}
}

type VectorStorer interface {
	Upsert(id string, vec []float64, metadata map[string]string)
	VecSearch(query []float64, topK int, filter func(id string, vec []float64, metadata map[string]string) bool) []VectorSearchHit
}

type VectorSearchHit struct {
	ID       string
	Score    float64
	Metadata map[string]string
}

type discoverFunc func(ctx context.Context, userID, themeList string, a discovererArgs) ([]map[string]interface{}, []map[string]interface{}, error)

func (td *ThemeDiscoverer) Name() string { return "theme_discoverer" }
func (td *ThemeDiscoverer) Description() string {
	return "从用户最近活动中发现值得长期追踪的候选主题"
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
	Created  []map[string]interface{} `json:"created"`
	Upgraded []map[string]interface{} `json:"upgraded"`
	Rejected []map[string]interface{} `json:"rejected"`
}

// Execute 是主题发现的唯一入口，完整流水线：
//  1. LLM 发现 candidates（conversation 模式直接从消息提取，stats 模式 ReAct 检索）
//  2. dedupBySimilarity — 向量去重 + 聚类升级（核心）
//  3. 为通过去重的 candidate 创建 draft → 异步写向量
//  4. checkDraftUpgrade — 名称分组兜底升级（处理无向量的老 draft）
//     返回 {created, upgraded, rejected}
func (td *ThemeDiscoverer) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	var a discovererArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &tool.Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}

	// 获取已有主题列表，注入 LLM prompt 避免重复提名
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

	// 步骤 2：向量去重 + 聚类升级
	// dedup 返回的 candidates 已标注 _action（merge_draft/reactivate）和 _matching_theme_name
	// upgraded 是在 dedup 中直接完成的升级（满足聚类条件），不需要再到步骤 4 处理
	upgraded := []map[string]interface{}{}
	if td.embedder != nil && td.vectorStore != nil && len(candidates) > 0 {
		candidates, rejected, upgraded = td.dedupBySimilarity(ec.Ctx, ec.UserID, candidates, rejected)
	}

	// 步骤 3：为通过去重的 candidate 创建 draft 记录
	result := DiscoverResult{Rejected: rejected, Upgraded: upgraded}
	for _, c := range candidates {
		name, _ := c["name"].(string)
		desc, _ := c["description"].(string)
		if name == "" {
			continue
		}

		action, _ := c["_action"].(string)

		// 名称查重（兜底 dedup 漏掉的情况：无向量的老主题）
		existing, _ := td.themeStore.GetByName(ec.Ctx, ec.UserID, name)
		if existing != nil {
			switch existing.Status {
			case "active":
				log.Printf("[theme] draft skipped: name=%s already active", name)
				result.Rejected = append(result.Rejected, map[string]interface{}{
					"direction": name, "reject_reason": "已有 active 主题",
				})
				continue
			case "draft":
				log.Printf("[theme] draft nominate again: name=%s existing_draft=%s", name, existing.ID[:8])
			case "closed":
				// dedup 已经处理过 reactivate 的情况，这里只兜底同名但无向量的
				if action != "reactivate" {
					if err := td.themeStore.ReactivateDraft(ec.Ctx, existing.ID, ec.UserID); err != nil {
						log.Printf("[theme] reactivate draft error: %v", err)
					} else {
						log.Printf("[theme] draft reactivated: name=%s from closed=%s", name, existing.ID[:8])
					}
				}
			}
		}

		duration, _ := c["duration"].(string)

		metaJSON, _ := json.Marshal(map[string]interface{}{"candidate_info": c})
		metaStr := string(metaJSON)
		theme := &model.ThemeState{
			ID:          uuid.New().String(),
			UserID:      ec.UserID,
			Name:        name,
			Description: desc,
			Status:      "draft",
			Duration:    duration,
			Metadata:    &metaStr,
		}
		if err := td.themeStore.Create(ec.Ctx, theme); err != nil {
			log.Printf("[theme] create draft error: %v", err)
			continue
		}
		log.Printf("[theme] draft created: name=%s id=%s duration=%s", name, theme.ID[:8], duration)

		// 异步写向量到 vectorStore + embedding 表（复用 ConceptEmbedding 先查后存）
		if td.themeEmbedder != nil {
			go td.themeEmbedder.EnsureEmbedded(context.Background(), ec.UserID, theme.ID, name, desc)
		}

		result.Created = append(result.Created, map[string]interface{}{
			"id":     theme.ID,
			"name":   name,
			"status": "draft",
		})
	}

	// 步骤 4：名称分组兜底升级（处理无向量老数据，新数据已在步骤 2 的聚类中升级）
	result.Upgraded = append(result.Upgraded, td.checkDraftUpgrade(ec.Ctx, ec.UserID)...)

	data, _ := json.Marshal(result)
	log.Printf("[theme_discoverer] created=%d upgraded=%d rejected=%d", len(result.Created), len(result.Upgraded), len(result.Rejected))
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

func (td *ThemeDiscoverer) discoverByConversation(ctx context.Context, userID, themeList string, a discovererArgs) ([]map[string]interface{}, []map[string]interface{}, error) {
	prompt := themeDiscovererPrompt + themeList + "\n请直接从以下对话消息中判断是否有值得提名的候选主题。不要调检索工具。\n"
	resp, err := td.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: prompt,
		Messages:     []llm.Message{{Role: "user", Content: a.Messages}},
		UserID:       userID,
	})
	if err != nil {
		return nil, nil, err
	}
	var output struct {
		Candidates []map[string]interface{} `json:"candidates"`
		Rejected   []map[string]interface{} `json:"rejected"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		log.Printf("[theme_discoverer] conversation parse error: %v", parseErr)
		return nil, nil, nil
	}
	return output.Candidates, output.Rejected, nil
}

func (td *ThemeDiscoverer) discoverByStats(ctx context.Context, userID, themeList string, a discovererArgs) ([]map[string]interface{}, []map[string]interface{}, error) {
	var sb strings.Builder
	sb.WriteString(themeList)
	sb.WriteString(fmt.Sprintf("\n## 最近 7 天活动概览\n"))
	sb.WriteString(fmt.Sprintf("- 新增消息：%d 条（含 user role）\n", a.RecentMsgCount))
	sb.WriteString(fmt.Sprintf("- 新增笔记：%d 条\n", a.RecentNoteCount))
	sb.WriteString(fmt.Sprintf("- 新增记忆：%d 条\n", a.RecentMemCount))
	sb.WriteString("\n请检索并判断是否有值得提名的候选主题。\n")

	loop := NewReactLoop(td.flashClient, td.retrievalTools, themeDiscovererPrompt, td.maxIterations, userID, "ThemeDiscoverer", CtxTraceRepo(ctx), CtxTraceID(ctx))
	result, err := loop.Run(ctx, sb.String())
	if err != nil {
		return nil, nil, err
	}
	var output struct {
		Candidates []map[string]interface{} `json:"candidates"`
		Rejected   []map[string]interface{} `json:"rejected"`
	}
	if _, parseErr := ParseFlashJSON(result.FinalResponse, &output); parseErr != nil {
		return nil, nil, fmt.Errorf("parse: %w", parseErr)
	}
	return output.Candidates, output.Rejected, nil
}

// dedupBySimilarity 是主题发现的核心算法：向量去重 + 聚类升级。
//
// 对每个 LLM 产出的 candidate：
//   1. embed candidate 的 name+desc
//   2. VecSearch 在 vectorStore 中搜语义相似的主题向量（type=theme, same user）
//   3. 遍历所有 score >= dedupThreshold 的命中，从 metadata["theme_id"] 反查 DB 获取状态
//   4. 按聚类中各类主题的状态分流：
//      - 存在 active → 拒绝（已在跟踪）
//      - 存在 closed → 重新激活为 draft
//      - 收集所有 draft → 构成聚类簇（+ 当前 candidate = clusterCount）
//   5. 聚类升级判断：
//      - clusterCount >= nominateCountThreshold（默认 3）AND
//        spanDays >= spanDaysThreshold（最早 draft 的 created_at 距今，默认 3 天）
//      - 满足 → 选最早 draft 升 active，其余 draft close，candidate 不入库
//      - 不满足 → candidate 标注 _action=merge_draft，用聚类名建新记录继续累计
//
// 返回 (kept candidates, rejected, upgraded)

func (td *ThemeDiscoverer) dedupBySimilarity(ctx context.Context, userID string, candidates, rejected []map[string]interface{}) ([]map[string]interface{}, []map[string]interface{}, []map[string]interface{}) {
	kept := make([]map[string]interface{}, 0, len(candidates))
	var upgraded []map[string]interface{}

	for _, c := range candidates {
		name, _ := c["name"].(string)
		desc, _ := c["description"].(string)

		// 1. embed candidate
		vec, err := td.embedder.Embed(ctx, name+" "+desc)
		if err != nil {
			log.Printf("[theme_discoverer] embed candidate %q error: %v", name, err)
			kept = append(kept, c)
			continue
		}

		// 2. VecSearch 搜语义相似的已有主题向量（topK=20，覆盖同用户所有主题）
		hits := td.vectorStore.VecSearch(vec, 20, func(id string, _ []float64, metadata map[string]string) bool {
			return metadata["type"] == "theme" && metadata["user_id"] == userID
		})

		// 3. 遍历命中，按状态分类收集
		hasActive := false
		var closedIDs []string
		type dinfo struct {
			id        string
			name      string
			createdAt time.Time
		}
		var drafts []dinfo
		var bestName string // 聚类中最有代表性的名字

		for _, hit := range hits {
			if hit.Score < td.dedupThreshold {
				continue // 相似度不够，跳过
			}
			tid := hit.Metadata["theme_id"]
			if tid == "" {
				// 无 theme_id 的老向量，保守按 active 处理（拒绝）
				hasActive = true
				break
			}
			t, err := td.themeStore.GetByID(ctx, tid, userID)
			if err != nil || t == nil {
				continue // DB 查不到，跳过
			}
			switch t.Status {
			case "active":
				hasActive = true
			case "draft":
				drafts = append(drafts, dinfo{id: t.ID, name: t.Name, createdAt: t.CreatedAt})
				if bestName == "" {
					bestName = t.Name
				}
			case "closed":
				closedIDs = append(closedIDs, t.ID)
				if bestName == "" {
					bestName = t.Name
				}
			}
			if hasActive {
				break
			}
		}

		// 4a. 已有 active → 拒绝
		if hasActive {
			log.Printf("[theme_discoverer] candidate %q rejected: similar to active theme", name)
			rejected = append(rejected, map[string]interface{}{
				"direction":     name,
				"reject_reason": fmt.Sprintf("向量去重：与已有 active 主题相似度 >= %.2f", td.dedupThreshold),
			})
			continue
		}

		// 4b. 重新激活 closed
		for _, cid := range closedIDs {
			if err := td.themeStore.ReactivateDraft(ctx, cid, userID); err != nil {
				log.Printf("[theme_discoverer] reactivate error: %v", err)
			} else {
				log.Printf("[theme_discoverer] reactivated closed theme %s", cid[:8])
			}
		}

		// 4c. 无任何匹配 → 全新主题
		if len(drafts) == 0 && len(closedIDs) == 0 {
			kept = append(kept, c)
			continue
		}

		// 5. 有匹配 draft → 聚类升级检查
		clusterCount := len(drafts) + 1 // +1 是当前 candidate
		if clusterCount >= td.nominateCountThreshold {
			// 计算聚类跨度：最早和最晚 draft 的 created_at 之差（与 checkDraftUpgrade 口径一致）
			var earliest, latest time.Time
			for _, d := range drafts {
				if earliest.IsZero() || d.createdAt.Before(earliest) {
					earliest = d.createdAt
				}
				if latest.IsZero() || d.createdAt.After(latest) {
					latest = d.createdAt
				}
			}
			spanDays := int(latest.Sub(earliest).Hours() / 24)

			log.Printf("[theme_discoverer] cluster check: candidate=%q cluster=%d span=%dd threshold=(%d,%d)",
				name, clusterCount, spanDays, td.nominateCountThreshold, td.spanDaysThreshold)

			// 满足升级条件 → 选最早 draft 升 active，其余 close，candidate 不入库
			if spanDays >= td.spanDaysThreshold {
				var oldest dinfo
				for _, d := range drafts {
					if oldest.id == "" || d.createdAt.Before(oldest.createdAt) {
						oldest = d
					}
				}
				log.Printf("[theme_discoverer] UPGRADE: candidate=%q -> oldest=%s(%s) cluster=%d span=%d",
					name, oldest.name, oldest.id[:8], clusterCount, spanDays)

				if err := td.themeStore.UpdateStatus(ctx, oldest.id, userID, "active"); err != nil {
					log.Printf("[theme_discoverer] upgrade error: %v", err)
				}
				for _, d := range drafts {
					if d.id != oldest.id {
						if err := td.themeStore.UpdateStatus(ctx, d.id, userID, "closed"); err != nil {
							log.Printf("[theme_discoverer] close draft %s error: %v", d.id[:8], err)
						}
					}
				}
				upgraded = append(upgraded, map[string]interface{}{
					"id": oldest.id, "name": oldest.name, "status": "active",
					"count": clusterCount, "span": spanDays,
				})
				rejected = append(rejected, map[string]interface{}{
					"direction":     name,
					"reject_reason": fmt.Sprintf("聚类升级：同簇 %d 条跨 %d 天，已升 active", clusterCount, spanDays),
				})
				// 升级完成，candidate 不入库（已在 rejected），跳过后续 merge_draft 逻辑
				continue
			}
		}

		// 聚类不满足升级条件 → merge_draft：用聚类名建新记录，继续累计
		c["_action"] = "merge_draft"
		c["_matching_theme_name"] = bestName
		kept = append(kept, c)
	}
	return kept, rejected, upgraded
}

// checkDraftUpgrade 按名称分组兜底升级（处理无向量老 draft 的情况）
func (td *ThemeDiscoverer) checkDraftUpgrade(ctx context.Context, userID string) []map[string]interface{} {
	drafts, err := td.themeStore.ListDrafts(ctx, userID)
	if err != nil {
		return nil
	}

	groups := map[string][]ThemeStateInfo{}
	for _, d := range drafts {
		groups[d.Name] = append(groups[d.Name], d)
	}

	var upgraded []map[string]interface{}
	for name, group := range groups {
		if len(group) < td.nominateCountThreshold {
			continue
		}
		span := group[len(group)-1].CreatedAt.Sub(group[0].CreatedAt)
		spanDays := int(span.Hours() / 24)

		if spanDays >= td.spanDaysThreshold {
			latest := group[len(group)-1]
			log.Printf("[theme] draft->active(fallback): name=%s count=%d span=%d", name, len(group), spanDays)
			if err := td.themeStore.UpdateStatus(ctx, latest.ID, userID, "active"); err != nil {
				continue
			}
			upgraded = append(upgraded, map[string]interface{}{
				"id": latest.ID, "name": name, "status": "active",
				"count": len(group), "span": spanDays,
			})
			for _, d := range group {
				if d.ID != latest.ID {
					if err := td.themeStore.UpdateStatus(ctx, d.ID, userID, "closed"); err != nil {
						log.Printf("[theme] close draft %s error: %v", d.ID[:8], err)
					}
				}
			}
		}
	}
	return upgraded
}
