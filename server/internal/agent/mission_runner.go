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
	"xhan-helper/internal/tool"
)

const missionRunnerSystemPrompt = `你在执行一个后台追踪任务（Mission）。你不需要向用户打招呼，不需要说"你好"。

当前时间: %s

## 任务定义

目标: %s

背景信息:
%s

上次检查状态:
%s

## 执行流程

1. 根据目标和背景，自行决定调用哪些工具查看当前数据状态
2. 对比上次检查状态（如果有），判断是否有值得关注的变化
3. 输出最终决定

## 输出格式（必须严格输出 JSON）

没有值得关注的变化时：
{"action": "skip", "reason": "no_change|no_data|already_known|not_yet", "new_summary": "一句话简述当前状态"}

有值得关注的变化时：
{"action": "report", "finding": {"title": "简明标题，15字以内", "content": "详细说明，100字以内", "urgency": "high|medium|low"}, "new_summary": "一句话简述当前状态"}

## 规则

- 不编造数据，找不到就坦诚说没找到
- 只输出 JSON，不要有多余文字
- 最多调用 3 轮工具，然后必须输出结果
- 如果数据太少无法判断，选择 skip 而不是编造发现`

// MissionRunner — 后台定期检查 Mission 并产出报告的运行器
type MissionRunner struct {
	flashClient    llm.LLMClient
	missionRepo    *service.MissionRepo
	mailboxRepo    *service.MailboxRepo
	retrievalTools map[string]tool.Tool
}

func NewMissionRunner(flashClient llm.LLMClient, missionRepo *service.MissionRepo, mailboxRepo *service.MailboxRepo, retrievalTools map[string]tool.Tool) *MissionRunner {
	return &MissionRunner{
		flashClient:    flashClient,
		missionRepo:    missionRepo,
		mailboxRepo:    mailboxRepo,
		retrievalTools: retrievalTools,
	}
}

type missionRunResult struct {
	Action     string          `json:"action"`
	Reason     string          `json:"reason"`
	Finding    *missionFinding `json:"finding,omitempty"`
	NewSummary string          `json:"new_summary"`
	Raw        string          `json:"-"`
}

type missionFinding struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Urgency string `json:"urgency"`
}

// Run 执行一次 Mission 检查
func (r *MissionRunner) Run(ctx context.Context, m *model.Mission) error {
	log.Printf("[mission_runner] running mission: %s goal=%s", m.ID[:8], m.Goal[:min(50, len(m.Goal))])

	now := time.Now().In(config.BJ).Format("2006-01-02 15:04")
	promptText := fmt.Sprintf(missionRunnerSystemPrompt, now, m.Goal, m.Context, m.LastSummary)

	reactLoop := NewReactLoop(r.flashClient, r.retrievalTools, promptText, 3, m.UserID,
		"mission_runner", nil, "")
	reactResult, err := reactLoop.Run(ctx, "请执行本次检查。")
	if err != nil {
		log.Printf("[mission_runner] react loop error: %v", err)
		return fmt.Errorf("react loop: %w", err)
	}

	// 解析 LLM 最终输出
	result := r.parseResult(reactResult.FinalResponse)

	// 根据结果投递或跳过
	if result.Action == "report" && result.Finding != nil {
		if err := r.deliverReport(ctx, m, result.Finding); err != nil {
			log.Printf("[mission_runner] deliver error: %v", err)
		}
	}

	// 更新 Mission 状态
	newSummary := result.NewSummary
	if newSummary == "" {
		newSummary = result.Reason
	}
	nextCheck := nextCheckTime(m.CheckCron)
	if err := r.missionRepo.UpdateAfterCheck(ctx, m.ID, newSummary, nextCheck); err != nil {
		log.Printf("[mission_runner] update after check error: %v", err)
	}

	log.Printf("[mission_runner] mission %s done: action=%s next=%s", m.ID[:8], result.Action, nextCheck.Format("01-02 15:04"))
	return nil
}

func (r *MissionRunner) parseResult(response string) *missionRunResult {
	// 尝试从响应中提取 JSON
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start == -1 || end == -1 || end <= start {
		return &missionRunResult{Action: "skip", Reason: "parse_error", NewSummary: "未能解析输出"}
	}

	jsonStr := response[start : end+1]
	var result missionRunResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		log.Printf("[mission_runner] parse error: %v raw=%s", err, response)
		return &missionRunResult{Action: "skip", Reason: "parse_error", Raw: response}
	}
	result.Raw = response
	return &result
}

func (r *MissionRunner) deliverReport(ctx context.Context, m *model.Mission, f *missionFinding) error {
	title := fmt.Sprintf("[%s] %s", trimTo(m.Goal, 20), f.Title)
	content := f.Content

	item := &model.MailboxItem{
		ID:         uuid.New().String(),
		UserID:     m.UserID,
		Title:      title,
		Content:    content,
		Type:       "mission_check",
		Source:     "任务追踪",
		EntityType: "mission",
		EntityID:   m.ID,
	}

	if err := r.mailboxRepo.Create(ctx, item); err != nil {
		return fmt.Errorf("create mailbox item: %w", err)
	}

	log.Printf("[mission_runner] delivered to mailbox: mission=%s title=%s", m.ID[:8], title)
	return nil
}

func trimTo(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

func nextCheckTime(cron string) time.Time {
	now := time.Now()
	switch cron {
	case "weekly":
		return now.Add(7 * 24 * time.Hour)
	case "2daily":
		return now.Add(2 * 24 * time.Hour)
	default: // daily
		return now.Add(24 * time.Hour)
	}
}
