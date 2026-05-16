package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const stateUpdaterPrompt = `你是 xhanHelper 的主题状态维护员。你会收到一个主题的当前状态页，以及用户最新的一段对话，你需要根据新对话更新状态页。

## 更新规则
1. 保留上一轮状态页中仍然有效的关键信息（规律、里程碑、待跟进事项）
2. 从新对话中提取新的事实、变化、决定，追加到状态页
3. 过期或已被新信息覆盖的事实用新发现替换
4. 每条关键信息标注大致时间
5. 控制篇幅：不超过 500 字
6. 超过 3 个月且本次未再出现的信息，可移除或标记为「可能已过期」

## 输出
{"updated_state": "更新后的状态页文本"}`

// StateUpdater — 增量更新主题状态页
type StateUpdater struct {
	flashClient llm.LLMClient
	msgRepo     *service.MessageRepo
	convRepo    *service.ConversationRepo
	themeStore  *ThemeStore
}

func NewStateUpdater(flashClient llm.LLMClient, msgRepo *service.MessageRepo, convRepo *service.ConversationRepo, themeStore *ThemeStore) *StateUpdater {
	return &StateUpdater{flashClient: flashClient, msgRepo: msgRepo, convRepo: convRepo, themeStore: themeStore}
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
		UpdatedState string `json:"updated_state"`
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

	// 更新 cursor
	lastMsg := msgs[len(msgs)-1]
	extra.Context.ThemeStateCursor = lastMsg.ID
	newExtra, _ := json.Marshal(extra)
	if err := s.convRepo.Update(ctx, &model.Conversation{
		ID: convID, Extra: string(newExtra),
	}); err != nil {
		log.Printf("[state_updater] update cursor error: %v", err)
	}

	log.Printf("[state_updater] updated theme %s, cursor=%s", themeID[:8], lastMsg.ID[:8])
	return nil
}
