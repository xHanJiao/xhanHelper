package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

const statePageUpdateRules = `1. 保留上一轮状态页中仍然有效的关键信息（规律、里程碑、待跟进事项）
2. 过期或已被新信息覆盖的事实用新发现替换
3. 每条关键信息标注大致时间
4. 控制篇幅：不超过 500 字
5. 超过 3 个月且本次未再出现的信息，可移除或标记为「可能已过期」`

const defaultVecThreshold = 0.5

var stateUpdaterReActPrompt = `你是 xhanHelper 的主题状态维护员。基于用户最近的对话，更新该主题的状态页。

## 核心原则

1. 诚实：没有新进展就不改。宁可保留现有状态页不变，不要编造
2. 简洁：状态页控制在 500 字以内，抓住变化和核心事实
3. 有据：所有结论必须来自对话或检索工具，不许编造
4. 增量：只关注对话中提到的新进展，保留旧状态中未被推翻的旧信息

## 工作方式

1. 首先阅读对话和旧状态页——根据对话判断是否有新进展
2. 如果有新信息需要记录，直接基于对话内容更新状态页
3. 如果需要交叉验证某个事实是否已记录过，可以随时调用检索工具
4. 如果对话内容足以判断，直接产出 JSON，不需要检索

## 可用检索工具（按需使用，不必每轮都调用）

1. memory_search.semantic_search(query) — 用自然语言句子向量检索长期记忆。query 必须是完整句子。也支持 search_by_keyword 关键词搜索、count_memories 统计、list_topics 查看主题
2. note.semantic_search(query) — 用自然语言句子向量检索笔记
3. message_search(query) — 在历史消息中按关键词搜索。query 传核心关键词
4. get_topic_detail(topic) — 获取指定话题下的所有笔记和记忆全文
5. get_bill_stats() — 获取账单统计摘要

## 检索策略

- 对话是第一数据源——优先从中提取新进展
- 仅在需要核实或补充背景时调检索工具
- 大多数情况下 1-2 轮即可完成，不需要多轮检索
- 产出最终状态页 JSON（不要再调工具）

## 输出（纯 JSON，除最终回复外不要有其他文字）
如果是最终回复，输出: {"updated_state": "状态页文本"}
如果没有任何值得更新的内容，输出: {"updated_state": ""}

` + statePageUpdateRules

// StateUpdater — 增量更新主题状态页（Phase 15: ReAct + 6 工具 + 检验）
type StateUpdater struct {
	flashClient        llm.LLMClient
	msgRepo            *service.MessageRepo
	convRepo           *service.ConversationRepo
	themeStore         *ThemeStore
	pendingFindingRepo *service.PendingFindingRepo
	vectorStore        app.VectorStore
	conceptEmbed       *ConceptEmbedService
	chunkBuilder       *ChunkBuilder
	retrievalTools     map[string]tool.Tool
	traceRepo          *service.ReflectionTraceRepo
}

func NewStateUpdater(flashClient llm.LLMClient, msgRepo *service.MessageRepo, convRepo *service.ConversationRepo, themeStore *ThemeStore, pendingFindingRepo *service.PendingFindingRepo, vectorStore app.VectorStore, conceptEmbed *ConceptEmbedService, chunkBuilder *ChunkBuilder, retrievalTools map[string]tool.Tool, traceRepo *service.ReflectionTraceRepo) *StateUpdater {
	return &StateUpdater{
		flashClient:        flashClient,
		msgRepo:            msgRepo,
		convRepo:           convRepo,
		themeStore:         themeStore,
		pendingFindingRepo: pendingFindingRepo,
		vectorStore:        vectorStore,
		conceptEmbed:       conceptEmbed,
		chunkBuilder:       chunkBuilder,
		retrievalTools:     retrievalTools,
		traceRepo:          traceRepo,
	}
}

// UpdateThemesFromConversation 用对话文本匹配活跃主题并增量更新状态页。
// 使用消息已有的向量（VectorStore 中 key=msg:{messageID}）做主题匹配，不额外嵌入。
// 自动创建 post_chat trace 包裹本次处理。
func (s *StateUpdater) UpdateThemesFromConversation(ctx context.Context, userID string, msgs []model.Message, convText string) {
	if convText == "" && len(msgs) == 0 {
		return
	}

	traceID := uuid.New().String()
	tr := &model.ReflectionTrace{
		ID: traceID, UserID: userID, TriggerType: "post_chat",
		StartedAt: time.Now(), Status: "running",
	}
	s.traceRepo.CreateTrace(ctx, tr)

	actives, err := s.themeStore.ListActives(ctx, userID)
	if err != nil || len(actives) == 0 {
		if err != nil {
			log.Printf("[state_updater] list actives error: %v", err)
		}
		return
	}

	// 从已有消息向量中拼 query，不重新嵌入（省 embedding 费用）
	matched := s.matchThemesFromMessages(msgs, userID)

	for _, t := range actives {
		score, ok := matched[t.ID]
		if !ok {
			log.Printf("[state_updater] theme %s (%s) not matched (score map has %d keys)", t.ID[:8], t.Name, len(matched))
			continue
		}
		if score < defaultVecThreshold {
			log.Printf("[state_updater] theme %s (%s) score %.3f < %.2f, skip", t.ID[:8], t.Name, score, defaultVecThreshold)
			continue
		}
		log.Printf("[state_updater] theme %s (%s) matched score %.3f, updating", t.ID[:8], t.Name, score)
		s.UpdateFromConversation(ctx, userID, t.ID, t.Name, convText, traceID)
	}

	log.Printf("[state_updater] conv matched %d/%d active themes", len(matched), len(actives))
	endTraceSafe(s.traceRepo, ctx, traceID, "completed", 0, 0, nil)
}

// UpdateFromConversation 对单个主题基于对话文本更新状态页，ReAct + 验证 + 矫正
func (s *StateUpdater) UpdateFromConversation(ctx context.Context, userID, themeID, themeName, convText string, traceID string) {
	log.Printf("[state_updater] UpdateFromConversation enter: theme=%s (%s)", themeID[:8], themeName)
	stateText := s.themeStore.LoadState(ctx, themeID, userID)
	name := s.themeStore.GetName(ctx, themeID, userID)
	if name == "" {
		name = themeName
	}

	inputMsg := fmt.Sprintf("## 主题：%s\n\n## 当前状态页\n%s\n\n## 最近对话\n%s", name, stateText, convText)

	const maxIterations = 10
	loop := NewReactLoop(s.flashClient, s.retrievalTools, stateUpdaterReActPrompt, maxIterations, userID, "StateUpdater", s.traceRepo, traceID)

	result, err := loop.Run(ctx, inputMsg)
	if err != nil {
		log.Printf("[state_updater] ReactLoop error: theme=%s err=%v", themeID[:8], err)
		return
	}
	log.Printf("[state_updater] ReactLoop done: theme=%s iters=%d finalRespLen=%d err=%v", themeID[:8], result.Iterations, len(result.FinalResponse), result.Error)

	var output struct {
		UpdatedState string `json:"updated_state"`
	}
	if _, parseErr := ParseFlashJSON(result.FinalResponse, &output); parseErr != nil || output.UpdatedState == "" {
		log.Printf("[state_updater] parse or empty: theme=%s err=%v", themeID[:8], parseErr)
		return
	}

	if err := s.themeStore.SaveState(ctx, themeID, userID, output.UpdatedState); err != nil {
		log.Printf("[state_updater] SaveState error: theme=%s err=%v", themeID[:8], err)
		return
	}
	ChunkStateText(ctx, s.chunkBuilder, userID, themeID, output.UpdatedState, name)
	log.Printf("[state_updater] updated theme %s (%s)", themeID[:8], name)
}

// matchThemesFromMessages 从消息向量中匹配活跃主题，合并 name+desc 和 chunk 两路搜索的最高分
func (s *StateUpdater) matchThemesFromMessages(msgs []model.Message, userID string) map[string]float64 {
	matched := make(map[string]float64)
	for _, m := range msgs {
		if m.Role != "user" || m.ID == "" {
			continue
		}
		vec, ok := s.vectorStore.GetVector("msg:" + m.ID)
		if !ok || len(vec) == 0 {
			continue
		}
		hits := s.vectorStore.Search(vec, 10, func(id string, _ []float64, meta map[string]string) bool {
			return meta["type"] == "theme" && meta["user_id"] == userID
		})
		chunkHits := s.vectorStore.Search(vec, 10, func(id string, _ []float64, meta map[string]string) bool {
			return meta["type"] == "chunk" && meta["parent_type"] == "theme" && meta["user_id"] == userID
		})
		for _, h := range hits {
			if tid := h.Metadata["theme_id"]; tid != "" {
				if existing, ok2 := matched[tid]; !ok2 || h.Score > existing {
					matched[tid] = h.Score
				}
			}
		}
		for _, h := range chunkHits {
			if tid := h.Metadata["parent_id"]; tid != "" {
				if existing, ok2 := matched[tid]; !ok2 || h.Score > existing {
					matched[tid] = h.Score
				}
			}
		}
	}
	return matched
}
