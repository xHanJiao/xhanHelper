package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// AgentLister — 获取用户智能体列表的接口（方便测试）
type AgentLister interface {
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Agent, int64, error)
}

// MessageCounter — 统计消息数量的接口
type MessageCounter interface {
	CountByRole(ctx context.Context, convID, role string) (int, error)
}

// AgentBinder — 绑定/换绑 agent 到会话
type AgentBinder interface {
	SetAgentID(ctx context.Context, convID, agentID string) error
	UpdateAgentID(ctx context.Context, convID string, agentID *string) error
}

// AutoRouteStep 异步 LLM 路由匹配最合适的智能体
type AutoRouteStep struct {
	flashClient    llm.LLMClient
	agentRepo      AgentLister
	currentAgentID *string
	convID         string
	msgCounter     MessageCounter
	interval       int // 每 N 条用户消息触发一次路由，0=禁用
	agentBinder    AgentBinder
}

func NewAutoRouteStep(flashClient llm.LLMClient, agentRepo AgentLister, currentAgentID *string, convID string, msgCounter MessageCounter, interval int, agentBinder AgentBinder) *AutoRouteStep {
	return &AutoRouteStep{
		flashClient:    flashClient,
		agentRepo:      agentRepo,
		currentAgentID: currentAgentID,
		convID:         convID,
		msgCounter:     msgCounter,
		interval:       interval,
		agentBinder:    agentBinder,
	}
}

func (s *AutoRouteStep) Name() string       { return "autoRoute" }
func (s *AutoRouteStep) Requires() []string { return []string{"raw_msg", "user_id"} }
func (s *AutoRouteStep) Produces() []string {
	return []string{"agent_id", "agent_system_prompt", "agent_name", "confidence"}
}

func (s *AutoRouteStep) ShouldRun(ctx context.Context) bool {
	pc := GetPreContext(ctx)
	if pc.UserSpecifiedAgent {
		return false
	}
	if s.interval <= 0 || s.convID == "" || s.msgCounter == nil {
		return false
	}
	count, err := s.msgCounter.CountByRole(ctx, s.convID, "user")
	if err != nil {
		log.Printf("[pipeline] autoRoute: count user messages error: %v", err)
		return false
	}
	// 第一条用户消息必触发，之后每 interval 条触发一次
	return count%s.interval == 0
}

// Run 异步路由——启动 goroutine，不阻塞管线，结果持久化到 DB 供下次请求使用
func (s *AutoRouteStep) Run(ctx context.Context) (context.Context, error) {
	pc := GetPreContext(ctx)

	if s.agentRepo == nil || s.flashClient == nil || s.agentBinder == nil {
		return ctx, nil
	}

	log.Printf("[pipeline] autoRoute: launching async route (userID=%s, convID=%s)", pc.UserID, s.convID)

	// 拷贝必要值，goroutine 内不依赖管线 context
	userID := pc.UserID
	rawMsg := pc.RawMsg
	convID := s.convID

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		agents, _, err := s.agentRepo.ListByUser(bgCtx, userID, 50, 0)
		if err != nil {
			log.Printf("[pipeline] autoRoute(async): ListByUser error: %v", err)
			return
		}
		if len(agents) == 0 {
			return
		}

		var agentLines strings.Builder
		for _, a := range agents {
			summary := a.Summary
			if summary == "" {
				summary = truncateSummary(a.SystemPrompt, 60)
			}
			agentLines.WriteString(fmt.Sprintf("- %s: %s\n", a.ID, a.Name))
			if summary != "" {
				agentLines.WriteString(fmt.Sprintf("  描述: %s\n", summary))
			}
		}

		routePrompt := fmt.Sprintf(
			`用户消息: "%s"

可用智能体:
%s
判断最匹配的智能体，如果不匹配任何智能体返回 none。
只输出 JSON: {"agent_id":"<id>","confidence":0.95} 或 {"agent_id":"none","confidence":0}`,
			rawMsg, agentLines.String(),
		)

		llmStart := time.Now()
		resp, err := s.flashClient.ChatSync(bgCtx, &llm.ChatRequest{
			SystemPrompt: "你是智能体路由器。根据用户消息判断最匹配的智能体。只输出 JSON，不要有其他内容。",
			Messages:     []llm.Message{{Role: "user", Content: routePrompt}},
		})
		if err != nil {
			log.Printf("[pipeline] autoRoute(async): LLM error: %v", err)
			return
		}
		log.Printf("[pipeline] autoRoute(async): LLM done in %v", time.Since(llmStart))

		var result struct {
			AgentID    string  `json:"agent_id"`
			Confidence float64 `json:"confidence"`
		}
		if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
			extracted := extractJSON(resp.Content)
			if extracted == "" {
				log.Printf("[pipeline] autoRoute(async): no JSON in response")
				return
			}
			if err := json.Unmarshal([]byte(extracted), &result); err != nil {
				log.Printf("[pipeline] autoRoute(async): parse error: %v", err)
				return
			}
		}

		if result.AgentID == "" || result.AgentID == "none" || result.Confidence < 0.8 {
			log.Printf("[pipeline] autoRoute(async): no match (agent_id=%s, confidence=%.2f)", result.AgentID, result.Confidence)
			return
		}

		for _, a := range agents {
			if a.ID == result.AgentID {
				if err := s.agentBinder.UpdateAgentID(bgCtx, convID, &a.ID); err != nil {
					log.Printf("[pipeline] autoRoute(async): UpdateAgentID error: %v", err)
					return
				}
				log.Printf("[pipeline] autoRoute(async): bound agent %q to conv %s (confidence=%.2f)", a.Name, convID, result.Confidence)
				return
			}
		}
		log.Printf("[pipeline] autoRoute(async): agent_id %q not found in user's agents", result.AgentID)
	}()

	return ctx, nil
}
