package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const stateUpdaterPrompt = `你是 xhanHelper 的主题状态维护员。你会收到一个主题的当前状态页，以及用户最新的一段对话，你需要根据新对话更新状态页，并判断是否有值得提醒用户的内容。

## 更新规则
1. 保留上一轮状态页中仍然有效的关键信息（规律、里程碑、待跟进事项）
2. 从新对话中提取新的事实、变化、决定，追加到状态页
3. 过期或已被新信息覆盖的事实用新发现替换
4. 每条关键信息标注大致时间
5. 控制篇幅：不超过 500 字
6. 超过 3 个月且本次未再出现的信息，可移除或标记为「可能已过期」

## 提醒判定

更新完状态页后，对照新旧状态判断是否需要提醒用户。仅当以下任一条件满足且变化足够显著时，才产出 finding：

1. 是否有显著偏离了之前记录的正常状态？
2. 是否有状态上的结构性推进？
3. 是否出现了需要关注的新外部信息？
4. 是否之前散落的数据点现在能串成规律了？

命中任一 → 输出 finding。全不命中 → finding 留空，输出 skip_reason。

## 输出
{"updated_state": "更新后的状态页文本", "finding": {"title": "...", "content": "...", "urgency": "high|medium|low", "source_refs": ["..."]}, "skip_reason": "no_deviation|no_milestone|insufficient_signal|null"}
finding 非空时 skip_reason 留 null，finding 为空时 skip_reason 必填。`

// StateUpdater — 增量更新主题状态页
type StateUpdater struct {
	flashClient        llm.LLMClient
	msgRepo            *service.MessageRepo
	convRepo           *service.ConversationRepo
	themeStore         *ThemeStore
	pendingFindingRepo *service.PendingFindingRepo
}

func NewStateUpdater(flashClient llm.LLMClient, msgRepo *service.MessageRepo, convRepo *service.ConversationRepo, themeStore *ThemeStore, pendingFindingRepo *service.PendingFindingRepo) *StateUpdater {
	return &StateUpdater{flashClient: flashClient, msgRepo: msgRepo, convRepo: convRepo, themeStore: themeStore, pendingFindingRepo: pendingFindingRepo}
}

// UpdateFromConversation 从绑定主题的会话中增量更新状态页
func (s *StateUpdater) UpdateFromConversation(ctx context.Context, userID, convID, themeID string) error {
	// 读 cursor
	extra, err := s.convRepo.GetExtra(ctx, convID)
	if err != nil {
		return fmt.Errorf("GetExtra: %w", err)
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
		sb += fmt.Sprintf("[%s]: %s\n", m.CreatedAt.Format("2006-01-02 15:04"), m.Content)
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
		return fmt.Errorf("ChatSync: %w", err)
	}

	var output struct {
		UpdatedState string                 `json:"updated_state"`
		Finding      map[string]interface{} `json:"finding"`
		SkipReason   string                 `json:"skip_reason"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		return fmt.Errorf("parse: %w", parseErr)
	}
	if output.UpdatedState == "" {
		return nil
	}

	// 更新状态页
	if err := s.themeStore.SaveState(ctx, themeID, userID, output.UpdatedState); err != nil {
		return fmt.Errorf("SaveState: %w", err)
	}

	// 暂存 finding（如有）
	if output.Finding != nil && len(output.Finding) > 0 {
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
