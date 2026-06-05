package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
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
- context 必须只包含该子领域的相关信息，严禁把整个状态页复制进去
- 如果一个子领域是纯状态记录（如「已确立某原则」），没有可追踪的变化指标，不要创建 Mission
- check_cron: 高频变化用 daily，长期事项用 weekly
- 宁可少也不要凑数。明显不值得追踪的不要硬创建
- 结合当前时间判断：如果状态页里的最后更新时间距离当前时间很久（超过2周），说明该子领域可能已停滞，不需要创建 Mission`

const missionRankPrompt = `你是一个任务优先级排序助手。以下是系统从各个主题中新发现的 Mission 候选，以及用户已有的活跃 Mission。

## 已有 Mission
%s

## 新候选
%s

## 任务

1. **去重**：如果某个候选与已有 Mission 追踪的是同一件事（即使措辞不同），移除它
2. **排序**：按重要性和紧迫度排序剩余候选
3. **截断**：只保留最重要的 3 个

## 输出格式（严格 JSON 数组）
[
  {"goal": "...", "context": "...", "tools_hint": "...", "check_cron": "..."},
  ...
]

最多 3 个，如果没有值得创建的返回 []。`

// MissionDiscoverer — 从活跃主题中发现可追踪的 Mission
type MissionDiscoverer struct {
	proClient   llm.LLMClient
	themeStore  *ThemeStore
	missionRepo *service.MissionRepo
	embedder    *MissionEmbedder
}

func NewMissionDiscoverer(proClient llm.LLMClient, themeStore *ThemeStore, missionRepo *service.MissionRepo, embedder *MissionEmbedder) *MissionDiscoverer {
	return &MissionDiscoverer{
		proClient:   proClient,
		themeStore:  themeStore,
		missionRepo: missionRepo,
		embedder:    embedder,
	}
}

// missionCandidate — LLM 输出的单个 Mission 候选
type missionCandidate struct {
	Goal      string `json:"goal"`
	Context   string `json:"context"`
	ToolsHint string `json:"tools_hint"`
	CheckCron string `json:"check_cron"`
}

type candidateWithTheme struct {
	candidate missionCandidate
	themeID   string
}

// Discover 扫活跃主题 → 收集所有候选 → LLM 去重排序 → 取 top 3 直接激活
func (d *MissionDiscoverer) Discover(ctx context.Context, userID string) (created int, err error) {
	themes, err := d.themeStore.ListActives(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("list themes: %w", err)
	}
	if len(themes) == 0 {
		return 0, nil
	}

	// Phase 1: 从所有活跃主题收集候选
	var all []candidateWithTheme
	for _, theme := range themes {
		candidates, err := d.evaluateTheme(ctx, userID, &theme)
		if err != nil {
			log.Printf("[mission_discoverer] evaluate theme %s error: %v", theme.Name, err)
			continue
		}
		for _, c := range candidates {
			all = append(all, candidateWithTheme{candidate: c, themeID: theme.ID})
		}
	}

	if len(all) == 0 {
		log.Printf("[mission_discoverer] no candidates from %d themes", len(themes))
		return 0, nil
	}

	log.Printf("[mission_discoverer] collected %d candidates from %d themes", len(all), len(themes))

	// Phase 2: 去重 + 排序，取 top 3
	topCandidates, err := d.rankAndDedup(ctx, userID, all)
	if err != nil {
		log.Printf("[mission_discoverer] rank error: %v", err)
		return 0, nil
	}

	// Phase 3: 创建（直接 active）
	for _, c := range topCandidates {
		m := &model.Mission{
			ID:        uuid.New().String(),
			UserID:    userID,
			Goal:      c.candidate.Goal,
			Context:   c.candidate.Context,
			ToolsHint: c.candidate.ToolsHint,
			CheckCron: c.candidate.CheckCron,
			Status:    "active",
			Source:    "auto",
		}
		themeID := c.themeID
		m.ThemeID = &themeID

		if err := d.missionRepo.Create(ctx, m); err != nil {
			log.Printf("[mission_discoverer] create error: %v", err)
			continue
		}

		go func(m *model.Mission) {
			if err := d.embedder.Upsert(ctx, m); err != nil {
				log.Printf("[mission_discoverer] embed error: %v", err)
			}
		}(m)

		created++
		log.Printf("[mission_discoverer] activated mission: %s goal=%s", m.ID[:8], m.Goal[:min(50, len(m.Goal))])
	}

	log.Printf("[mission_discoverer] discover done: candidates=%d activated=%d", len(all), created)
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

	for i := range candidates {
		if candidates[i].CheckCron == "" {
			candidates[i].CheckCron = "daily"
		}
	}

	return candidates, nil
}

func (d *MissionDiscoverer) rankAndDedup(ctx context.Context, userID string, candidates []candidateWithTheme) ([]candidateWithTheme, error) {
	// 加载已有活跃 Mission
	existing, err := d.missionRepo.ListActiveByUser(ctx, userID)
	if err != nil {
		log.Printf("[mission_discoverer] list existing error: %v", err)
		existing = nil
	}

	// 构建已有 Mission 列表
	var existingLines []string
	for _, m := range existing {
		existingLines = append(existingLines, fmt.Sprintf("- %s", m.Goal))
	}
	existingText := "（无）"
	if len(existingLines) > 0 {
		existingText = strings.Join(existingLines, "\n")
	}

	// 构建候选列表（带索引）
	var candidateLines []string
	for i, c := range candidates {
		candidateLines = append(candidateLines, fmt.Sprintf("[%d] goal: %s | cron: %s | tools: %s", i, c.candidate.Goal, c.candidate.CheckCron, c.candidate.ToolsHint))
	}
	candidatesText := strings.Join(candidateLines, "\n")

	prompt := fmt.Sprintf(missionRankPrompt, existingText, candidatesText)

	req := &llm.ChatRequest{
		SystemPrompt: "你是一个任务优先级排序助手。只输出 JSON 数组，不要有任何其他内容。",
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		UserID: userID,
	}

	msg, err := d.proClient.ChatSync(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("rank LLM call: %w", err)
	}

	var ranked []missionCandidate
	if err := json.Unmarshal([]byte(msg.Content), &ranked); err != nil {
		log.Printf("[mission_discoverer] rank parse error: %v content=%s", err, msg.Content)
		return nil, nil
	}

	// 将排序结果映射回 candidateWithTheme（通过 goal 匹配）
	var result []candidateWithTheme
	for _, r := range ranked {
		for _, c := range candidates {
			if c.candidate.Goal == r.Goal {
				c.candidate = r // 用 LLM 可能微调后的版本
				result = append(result, c)
				break
			}
		}
	}

	if len(result) == 0 && len(ranked) > 0 {
		// goal 没精确匹配上，直接用 LLM 输出创建（不带 themeID）
		for _, r := range ranked {
			result = append(result, candidateWithTheme{candidate: r})
		}
	}

	return result, nil
}
