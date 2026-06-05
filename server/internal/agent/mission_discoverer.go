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

const missionDiscovererPrompt = `你在评估一个用户主题是否可以升级为「持续追踪任务（Mission）」。

背景：系统已从用户的对话/行为中自动发现了这个主题，并生成了状态描述。你需要判断它是否值得创建为一个后台定期检查的追踪任务。

## 判断标准

1. 持续性：这个主题是否代表一个持续关注的事项？（非一次性事件）
2. 主动性：用户是否表现出主动关注？（多次提及、记录、询问）
3. 可追踪性：是否有可量化的指标？（笔记更新、账单变化、对话频率等）

## 输出格式

如果值得追踪：
{"should_create": true, "goal": "一句话描述任务目标，突出要追踪什么变化", "tools_hint": "建议检索工具（note,message_search,bill 等，逗号分隔）", "check_cron": "daily|weekly"}

如果不值得追踪：
{"should_create": false, "reason": "no_sustainability|no_engagement|no_metrics|trivial", "detail": "简短说明原因"}

注意：
- goal 要具体可操作，不只是「关注XX」，而是「追踪XX的进展/变化」
- check_cron 根据主题的动态性判断：高频变化的用 daily，长期事项用 weekly
- 宁可说 no 也不要说 yes。明显不值得追踪的不要硬创建`

// MissionDiscoverer — 从活跃主题中发现可追踪的 Mission
type MissionDiscoverer struct {
	flashClient llm.LLMClient
	themeStore  *ThemeStore
	missionRepo *service.MissionRepo
}

func NewMissionDiscoverer(flashClient llm.LLMClient, themeStore *ThemeStore, missionRepo *service.MissionRepo) *MissionDiscoverer {
	return &MissionDiscoverer{
		flashClient: flashClient,
		themeStore:  themeStore,
		missionRepo: missionRepo,
	}
}

type missionDiscoverResult struct {
	ShouldCreate bool   `json:"should_create"`
	Goal         string `json:"goal"`
	ToolsHint    string `json:"tools_hint"`
	CheckCron    string `json:"check_cron"`
	Reason       string `json:"reason"`
	Detail       string `json:"detail"`
}

// Discover 扫活跃主题，对没有 Mission 的候选调 LLM 判断是否创建
func (d *MissionDiscoverer) Discover(ctx context.Context, userID string) (created int, err error) {
	themes, err := d.themeStore.ListActives(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("list themes: %w", err)
	}
	if len(themes) == 0 {
		return 0, nil
	}

	for _, theme := range themes {
		// 已经有关联的 Mission 则跳过
		existing, err := d.missionRepo.ListByTheme(ctx, theme.ID)
		if err != nil {
			log.Printf("[mission_discoverer] ListByTheme error: %v", err)
			continue
		}
		if len(existing) > 0 {
			continue
		}

		// 调 LLM 判断
		m, ok := d.evaluateTheme(ctx, userID, &theme)
		if !ok || m == nil {
			continue
		}

		m.ID = uuid.New().String()
		m.UserID = userID
		themeID := theme.ID
		m.ThemeID = &themeID
		m.Status = "suggested"
		m.Source = "auto"

		if err := d.missionRepo.Create(ctx, m); err != nil {
			log.Printf("[mission_discoverer] create error: %v", err)
			continue
		}

		created++
		log.Printf("[mission_discoverer] discovered mission: %s theme=%s goal=%s",
			m.ID[:8], theme.Name, m.Goal[:min(50, len(m.Goal))])
	}

	log.Printf("[mission_discoverer] discover done: scanned=%d created=%d", len(themes), created)
	return created, nil
}

func (d *MissionDiscoverer) evaluateTheme(ctx context.Context, userID string, theme *ThemeStateInfo) (*model.Mission, bool) {
	prompt := fmt.Sprintf("%s\n\n## 主题信息\n名称: %s\n描述: %s\n状态: %s\n持续时长: %s\n状态页: %s",
		missionDiscovererPrompt, theme.Name, theme.Description, theme.Status, theme.Duration, theme.StateText)

	req := &llm.ChatRequest{
		SystemPrompt: "你是一个智能助理的后台评估模块。只输出 JSON，不要有任何其他内容。",
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		UserID: userID,
	}

	msg, err := d.flashClient.ChatSync(ctx, req)
	if err != nil {
		log.Printf("[mission_discoverer] LLM call error for theme %s: %v", theme.Name, err)
		return nil, false
	}

	var result missionDiscoverResult
	if err := json.Unmarshal([]byte(msg.Content), &result); err != nil {
		log.Printf("[mission_discoverer] parse response error for theme %s: %v content=%s", theme.Name, err, msg.Content)
		return nil, false
	}

	if !result.ShouldCreate {
		log.Printf("[mission_discoverer] skip theme %s: reason=%s detail=%s", theme.Name, result.Reason, result.Detail)
		return nil, false
	}

	if result.CheckCron == "" {
		result.CheckCron = "daily"
	}

	context := fmt.Sprintf("主题: %s\n描述: %s\n状态: %s", theme.Name, theme.Description, theme.StateText)

	return &model.Mission{
		Goal:      result.Goal,
		Context:   context,
		ToolsHint: result.ToolsHint,
		CheckCron: result.CheckCron,
	}, true
}
