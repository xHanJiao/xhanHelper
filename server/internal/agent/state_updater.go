package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/google/uuid"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const statePageUpdateRules = `1. 保留上一轮状态页中仍然有效的关键信息（规律、里程碑、待跟进事项）
2. 过期或已被新信息覆盖的事实用新发现替换
3. 每条关键信息标注大致时间
4. 控制篇幅：不超过 500 字
5. 超过 3 个月且本次未再出现的信息，可移除或标记为「可能已过期」`

var stateUpdaterPrompt = fmt.Sprintf(`你是 xhanHelper 的主题状态维护员。你会收到一个主题的当前状态页，以及一条新提取的记忆，你需要根据新记忆更新状态页。

%s

## 输出
{"updated_state": "更新后的状态页文本"}`, statePageUpdateRules)

// StateUpdater — 增量更新主题状态页
type StateUpdater struct {
	flashClient        llm.LLMClient
	msgRepo            *service.MessageRepo
	convRepo           *service.ConversationRepo
	themeStore         *ThemeStore
	pendingFindingRepo *service.PendingFindingRepo
	vectorStore        app.VectorStore
	conceptEmbed       *ConceptEmbedService
	chunkBuilder       *ChunkBuilder
}

func NewStateUpdater(flashClient llm.LLMClient, msgRepo *service.MessageRepo, convRepo *service.ConversationRepo, themeStore *ThemeStore, pendingFindingRepo *service.PendingFindingRepo, vectorStore app.VectorStore, conceptEmbed *ConceptEmbedService, chunkBuilder *ChunkBuilder) *StateUpdater {
	return &StateUpdater{
		flashClient:        flashClient,
		msgRepo:            msgRepo,
		convRepo:           convRepo,
		themeStore:         themeStore,
		pendingFindingRepo: pendingFindingRepo,
		vectorStore:        vectorStore,
		conceptEmbed:       conceptEmbed,
		chunkBuilder:       chunkBuilder,
	}
}

// UpdateFromMemory 从一条新记忆中增量更新主题状态页。
func (s *StateUpdater) UpdateFromMemory(ctx context.Context, userID, themeID string, memory *model.UserMemory) error {
	stateText := s.themeStore.LoadState(ctx, themeID, userID)

	inputMsg := fmt.Sprintf("## 当前状态页\n%s\n\n## 新记忆（话题: %s, 标签: %s）\n%s", stateText, memory.Topic, memory.Tags, memory.MemoryValue)
	resp, err := s.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: stateUpdaterPrompt,
		Messages:     []llm.Message{{Role: "user", Content: inputMsg}},
		UserID:       userID,
	})
	if err != nil {
		return fmt.Errorf("ChatSync theme=%s memory=%s: %w", themeID[:8], memory.ID[:8], err)
	}

	var output struct {
		UpdatedState string `json:"updated_state"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		return fmt.Errorf("parse LLM output theme=%s memory=%s: %w", themeID[:8], memory.ID[:8], parseErr)
	}
	if output.UpdatedState == "" {
		return nil
	}

	if err := s.themeStore.SaveState(ctx, themeID, userID, output.UpdatedState); err != nil {
		return fmt.Errorf("SaveState theme=%s memory=%s: %w", themeID[:8], memory.ID[:8], err)
	}

	log.Printf("[state_updater] updated theme %s from memory %s", themeID[:8], memory.ID[:8])
	return nil
}

// UpdateThemesFromMemory 用一条新记忆匹配活跃主题，命中则增量更新。
func (s *StateUpdater) UpdateThemesFromMemory(ctx context.Context, userID string, memory *model.UserMemory) {
	if memory == nil || memory.Topic == "" {
		return
	}

	actives, err := s.themeStore.ListActives(ctx, userID)
	if err != nil {
		log.Printf("[state_updater] list actives error: %v", err)
		return
	}
	if len(actives) == 0 {
		return
	}

	hitIDs := s.recallMatchingThemes(ctx, userID, memory, actives)

	for _, t := range actives {
		if !hitIDs[t.ID] {
			continue
		}
		if err := s.UpdateFromMemory(ctx, userID, t.ID, memory); err != nil {
			log.Printf("[state_updater] update theme %s error: %v", t.ID[:8], err)
		}
	}

	log.Printf("[state_updater] memory topic=%q matched %d themes (active=%d)", memory.Topic, len(hitIDs), len(actives))
}

// recallMatchingThemes 三路并发召回：topic 匹配 + tag 嵌入 + 向量搜索，返回去重后的 theme ID 集合。
func (s *StateUpdater) recallMatchingThemes(ctx context.Context, userID string, memory *model.UserMemory, actives []ThemeStateInfo) map[string]bool {
	hitCh := make(chan themeHit, 30)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		s.recallByTopic(memory.Topic, actives, hitCh)
	}()
	go func() {
		defer wg.Done()
		s.recallByTags(ctx, userID, memory.Tags, hitCh)
	}()
	go func() {
		defer wg.Done()
		s.recallByVector(ctx, userID, memory.MemoryValue, hitCh)
	}()

	go func() {
		wg.Wait()
		close(hitCh)
	}()

	hitIDs := make(map[string]bool)
	for h := range hitCh {
		hitIDs[h.id] = true
	}
	return hitIDs
}

type themeHit struct{ id string }

// recallByTopic 第一路：记忆 topic 与主题名互相包含 → 直接命中。
func (s *StateUpdater) recallByTopic(topic string, actives []ThemeStateInfo, ch chan<- themeHit) {
	topicLower := strings.ToLower(topic)
	for _, t := range actives {
		nameLower := strings.ToLower(t.Name)
		if strings.Contains(nameLower, topicLower) || strings.Contains(topicLower, nameLower) {
			ch <- themeHit{id: t.ID}
		}
	}
}

// recallByTags 第二路：每个 tag embed 后搜主题向量 → 命中。
func (s *StateUpdater) recallByTags(ctx context.Context, userID, tags string, ch chan<- themeHit) {
	if tags == "" {
		return
	}
	for _, tag := range strings.Split(tags, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		vec, err := s.conceptEmbed.GetOrEmbed(ctx, "tag", tag, userID)
		if err != nil || len(vec) == 0 {
			continue
		}
		hits := s.vectorStore.Search(vec, 10, func(id string, _ []float64, meta map[string]string) bool {
			return meta["type"] == "theme" && meta["user_id"] == userID
		})
		for _, hit := range hits {
			if hit.Score >= defaultVecThreshold {
				if tid := hit.Metadata["theme_id"]; tid != "" {
					ch <- themeHit{id: tid}
				}
			}
		}
	}
}

const defaultVecThreshold = 0.5

// recallByVector 第三路：记忆文本 embedding → VectorStore.Search(type="theme") → 过阈值命中。
func (s *StateUpdater) recallByVector(ctx context.Context, userID, text string, ch chan<- themeHit) {
	if text == "" {
		return
	}

	vec, err := s.conceptEmbed.GetOrEmbed(ctx, "topic", text, userID)
	if err != nil || len(vec) == 0 {
		log.Printf("[state_updater] embed text for vector recall error: %v text=%q", err, text[:min(60, len(text))])
		return
	}

	hits := s.vectorStore.Search(vec, 20, func(id string, _ []float64, meta map[string]string) bool {
		return meta["type"] == "theme" && meta["user_id"] == userID
	})

	for _, hit := range hits {
		if hit.Score >= defaultVecThreshold {
			if tid := hit.Metadata["theme_id"]; tid != "" {
				ch <- themeHit{id: tid}
			}
		}
	}
}

// UpdateFromConversation 从绑定主题的会话中增量更新状态页。
// 注：Phase 13 改为记忆驱动，此方法当前未使用。保留以备需要。
func (s *StateUpdater) updateFromConvLegacy(ctx context.Context, userID, convID, themeID string) error {
	// 读 cursor
	extra, err := s.convRepo.GetExtra(ctx, convID)
	if err != nil {
		return fmt.Errorf("GetExtra conv=%s: %w", convID[:8], err)
	}
	if extra == nil || extra.Context == nil || extra.Context.ThemeID == "" {
		return nil
	}

	// 拉新消息
	var msgs []model.Message
	if extra.Context.ThemeStateCursor != "" {
		msgs, err = s.msgRepo.ListByConversationSince(ctx, convID, extra.Context.ThemeStateCursor, 200)
	} else {
		msgs, err = s.msgRepo.ListByConversation(ctx, convID, 200)
	}
	if err != nil || len(msgs) == 0 {
		return err
	}

	// 拼 user 消息
	var sb string
	for _, m := range msgs {
		if m.Role == "tool" {
			continue
		}
		sb += fmt.Sprintf("[%s]: %s\n", m.CreatedAt.In(config.BJ).Format("2006-01-02 15:04"), m.Content)
	}
	if sb == "" {
		return nil
	}

	// 读当前 state_text
	stateText := s.themeStore.LoadState(ctx, themeID, userID)

	// 调 LLM 更新
	inputMsg := fmt.Sprintf("## 当前状态页\n%s\n\n## 新对话\n%s", stateText, sb)
	resp, err := s.flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: stateUpdaterPrompt,
		Messages:     []llm.Message{{Role: "user", Content: inputMsg}},
		UserID:       userID,
	})
	if err != nil {
		return fmt.Errorf("ChatSync theme=%s conv=%s: %w", themeID[:8], convID[:8], err)
	}

	var output struct {
		UpdatedState string                 `json:"updated_state"`
		Finding      map[string]interface{} `json:"finding"`
		SkipReason   string                 `json:"skip_reason"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		return fmt.Errorf("parse LLM output theme=%s conv=%s: %w", themeID[:8], convID[:8], parseErr)
	}
	if output.UpdatedState == "" {
		return nil
	}

	if err := s.themeStore.SaveState(ctx, themeID, userID, output.UpdatedState); err != nil {
		return fmt.Errorf("SaveState theme=%s conv=%s: %w", themeID[:8], convID[:8], err)
	}

	// 暂存 finding（如有）
	if len(output.Finding) > 0 {
		title, _ := output.Finding["title"].(string)
		content, _ := output.Finding["content"].(string)
		urgency, _ := output.Finding["urgency"].(string)
		refsJSON, _ := json.Marshal(output.Finding["source_refs"])

		themeName := s.themeStore.GetName(ctx, themeID, userID)

		f := &model.PendingFinding{
			ID:         uuid.New().String(),
			UserID:     userID,
			ThemeID:    themeID,
			ThemeName:  themeName,
			Title:      title,
			Content:    content,
			Urgency:    urgency,
			SourceRefs: string(refsJSON),
		}
		if err := s.pendingFindingRepo.Create(ctx, f); err != nil {
			log.Printf("[state_updater] create pending finding error: %v", err)
		} else {
			log.Printf("[state_updater] finding stored for theme %s", themeID[:8])
		}
	} else if output.SkipReason != "" {
		log.Printf("[state_updater] theme %s: no finding, reason=%s", themeID[:8], output.SkipReason)
	}

	// 更新 cursor
	lastMsg := msgs[len(msgs)-1]
	extra.Context.ThemeStateCursor = lastMsg.ID
	newExtra, _ := json.Marshal(extra)
	if err := s.convRepo.UpdateExtra(ctx, convID, string(newExtra)); err != nil {
		log.Printf("[state_updater] update cursor error: %v", err)
	}

	log.Printf("[state_updater] updated theme %s, cursor=%s", themeID[:8], lastMsg.ID[:8])
	return nil
}

// UpdateThemesFromConversation 用对话文本匹配活跃主题并增量更新状态页。
// Phase 15: 替代 UpdateThemesFromMemory，输入从精炼 memory 改为原始对话。
// 使用消息已有的向量（VectorStore 中 key=msg:{messageID}）做主题匹配，不额外嵌入。
func (s *StateUpdater) UpdateThemesFromConversation(ctx context.Context, userID string, msgs []model.Message, convText string) {
	if convText == "" && len(msgs) == 0 {
		return
	}

	actives, err := s.themeStore.ListActives(ctx, userID)
	if err != nil || len(actives) == 0 {
		if err != nil {
			log.Printf("[state_updater] list actives error: %v", err)
		}
		return
	}

	// 从已有消息向量中拼 query，不重新嵌入（省 embedding 费用）
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
		for _, h := range hits {
			if tid := h.Metadata["theme_id"]; tid != "" {
				if existing, ok2 := matched[tid]; !ok2 || h.Score > existing {
					matched[tid] = h.Score
				}
			}
		}
	}

	for _, t := range actives {
		score, ok := matched[t.ID]
		if !ok || score < defaultVecThreshold {
			continue
		}
		s.UpdateFromConversation(ctx, userID, t.ID, t.Name, convText)
	}

	log.Printf("[state_updater] conv matched %d/%d active themes", len(matched), len(actives))
}

// UpdateFromConversation 对单个主题基于对话文本更新状态页，含验证机制。
func (s *StateUpdater) UpdateFromConversation(ctx context.Context, userID, themeID, themeName, convText string) {
	stateText := s.themeStore.LoadState(ctx, themeID, userID)
	name := s.themeStore.GetName(ctx, themeID, userID)
	if name == "" {
		name = themeName
	}

	inputMsg := fmt.Sprintf("## 主题\n%s\n\n## 当前状态页\n%s\n\n## 最近对话\n%s", name, stateText, convText)
	prompt := fmt.Sprintf(`你是 xhanHelper 的主题状态维护员。基于对话更新「%s」的状态页。

## 规则
1. 保留旧状态中仍然有效的关键信息
2. 从对话中提取新进展（决策、进度、发现）
3. 过期信息用新数据替换
4. 控制篇幅不超过 500 字

## 输出（纯 JSON）
{"updated_state": "更新后的状态页文本"}`, name)

	// 验证最多重试 2 次
	const maxRetries = 2
	for retry := 0; retry <= maxRetries; retry++ {
		resp, err := s.flashClient.ChatSync(ctx, &llm.ChatRequest{
			SystemPrompt: prompt,
			Messages:     []llm.Message{{Role: "user", Content: inputMsg}},
			UserID:       userID,
		})
		if err != nil {
			log.Printf("[state_updater] ChatSync error: theme=%s err=%v", themeID[:8], err)
			return
		}

		var output struct {
			UpdatedState string `json:"updated_state"`
		}
		if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil || output.UpdatedState == "" {
			log.Printf("[state_updater] parse error: theme=%s err=%v", themeID[:8], parseErr)
			return
		}

		// 检验 proposed
		vr, err := VerifyStateUpdate(ctx, s.flashClient, userID, stateText, convText, output.UpdatedState)
		if err != nil {
			log.Printf("[state_updater] verify error: theme=%s err=%v", themeID[:8], err)
			return
		}

		if vr.Complete {
			// 通过检验，写入 state_text + chunk
			if err := s.themeStore.SaveState(ctx, themeID, userID, output.UpdatedState); err != nil {
				log.Printf("[state_updater] SaveState error: theme=%s err=%v", themeID[:8], err)
				return
			}
			if s.chunkBuilder != nil {
				ChunkStateText(ctx, s.chunkBuilder, userID, themeID, output.UpdatedState)
			}
			log.Printf("[state_updater] updated theme %s (%s), retries=%d", themeID[:8], name, retry)
			return
		}

		if retry < maxRetries && vr.Suggestion != "" {
			inputMsg += fmt.Sprintf("\n\n## 上次更新未通过检验\n%s\n请根据以上建议重新生成。", vr.Suggestion)
			log.Printf("[state_updater] retry %d/%d for theme %s: %s", retry+1, maxRetries, themeID[:8], vr.Suggestion)
		}
	}

	log.Printf("[state_updater] theme %s: max retries reached, state not updated", themeID[:8])
}
