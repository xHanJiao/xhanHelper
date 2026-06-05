package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const missionDiscovererPrompt = `你在评估一个用户主题是否可以拆分为多个「持续追踪任务（Mission）」。

背景：系统已从用户的对话/行为中自动发现了这个主题，并生成了状态描述。一个主题可能包含多个可独立追踪的子领域，你需要逐一判断，各子领域可以独立追踪。

当前时间: %s

## 判断标准

1. 持续性：是否代表一个持续关注的事项？（非一次性事件）
2. 可追踪性：是否有可量化的指标？（笔记更新、账单变化、对话频率等）
3. 值得通知：变化是否值得主动提醒用户？

## 输出格式

从状态页中识别出值得追踪的子领域，每个子领域输出一个 Mission 候选。如果都不值得追踪，返回空数组。

[
  {
    "goal": "一句话描述任务目标，要具体可操作",
    "context": "仅提取状态页中与该子领域相关的原文片段，不要包含其他子领域的信息。供后续后台检查时使用。",
    "tools_hint": "建议检索工具（note,message_search,bill 等，逗号分隔）",
    "check_cron": "daily|weekly"
  },
  ...
]

如果都不值得追踪，返回空数组 []。

注意：
- goal 要具体可操作，不只是「关注XX」，而是「追踪XX的进展/变化」。例如：「追踪求职投递进度和面试反馈」而非「关注求职」
- context 必须只包含该子领域的相关信息，严禁把整个状态页复制进去。后续 Mission 检查时 LLM 只会看到这个 context
- 如果一个子领域是纯状态记录（如「已确立某原则」），没有可追踪的变化指标，不要创建 Mission
- check_cron: 高频变化用 daily，长期事项用 weekly
- 宁可少也不要凑数。明显不值得追踪的不要硬创建
- 结合当前时间判断：如果状态页里的最后更新时间距离当前时间很久（超过2周），说明该子领域可能已停滞，不需要创建 Mission`

// MissionDiscoverer — 从活跃主题中发现可追踪的 Mission
type MissionDiscoverer struct {
	proClient   llm.LLMClient
	themeStore  *ThemeStore
	missionRepo *service.MissionRepo
}

func NewMissionDiscoverer(proClient llm.LLMClient, themeStore *ThemeStore, missionRepo *service.MissionRepo) *MissionDiscoverer {
	return &MissionDiscoverer{
		proClient:   proClient,
		themeStore:  themeStore,
		missionRepo: missionRepo,
	}
}

// missionCandidate — LLM 输出的单个 Mission 候选
type missionCandidate struct {
	Goal      string `json:"goal"`
	Context   string `json:"context"`
	ToolsHint string `json:"tools_hint"`
	CheckCron string `json:"check_cron"`
}

// Discover 扫活跃主题，对每个主题调 LLM 提取可追踪的 Mission 候选
func (d *MissionDiscoverer) Discover(ctx context.Context, userID string) (created int, err error) {
	themes, err := d.themeStore.ListActives(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("list themes: %w", err)
	}
	if len(themes) == 0 {
		return 0, nil
	}

	for _, theme := range themes {
		// 调 LLM 提取候选
		candidates, err := d.evaluateTheme(ctx, userID, &theme)
		if err != nil {
			log.Printf("[mission_discoverer] evaluate theme %s error: %v", theme.Name, err)
			continue
		}

		for _, c := range candidates {
			m := &model.Mission{
				ID:        uuid.New().String(),
				UserID:    userID,
				Goal:      c.Goal,
				Context:   c.Context,
				ToolsHint: c.ToolsHint,
				CheckCron: c.CheckCron,
				Status:    "suggested",
				Source:    "auto",
			}
			themeID := theme.ID
			m.ThemeID = &themeID

			if err := d.missionRepo.Create(ctx, m); err != nil {
				log.Printf("[mission_discoverer] create error: %v", err)
				continue
			}

			created++
			log.Printf("[mission_discoverer] discovered mission: %s theme=%s goal=%s",
				m.ID[:8], theme.Name, m.Goal[:min(50, len(m.Goal))])
		}
	}

	log.Printf("[mission_discoverer] discover done: themes=%d created=%d", len(themes), created)
	return created, nil
}

func (d *MissionDiscoverer) evaluateTheme(ctx context.Context, userID string, theme *ThemeStateInfo) ([]missionCandidate, error) {
	now := time.Now().In(config.BJ).Format("2006-01-02 15:04")
	prompt := fmt.Sprintf(missionDiscovererPrompt, now)
	prompt += fmt.Sprintf("\n\n## 主题信息\n名称: %s\n描述: %s\n状态页:\n%s",
		theme.Name, theme.Description, theme.StateText)

	req := &llm.ChatRequest{
		SystemPrompt: "你是一个智能助理的后台评估模块。只输出 JSON 数组，不要有任何其他内容。",
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		UserID: userID,
	}

	msg, err := d.proClient.ChatSync(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM call: %w", err)
	}

	var candidates []missionCandidate
	if err := json.Unmarshal([]byte(msg.Content), &candidates); err != nil {
		log.Printf("[mission_discoverer] parse error for theme %s: %v content=%s", theme.Name, err, msg.Content)
		return nil, nil
	}

	if len(candidates) == 0 {
		log.Printf("[mission_discoverer] skip theme %s: no candidates", theme.Name)
		return nil, nil
	}

	for i := range candidates {
		if candidates[i].CheckCron == "" {
			candidates[i].CheckCron = "daily"
		}
	}

	log.Printf("[mission_discoverer] theme %s → %d candidates", theme.Name, len(candidates))
	return candidates, nil
}
