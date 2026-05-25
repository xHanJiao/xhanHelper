package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
)

// 意图关键词 — 短消息包含这些词时不跳过记忆检索
var intentKeywords = []string{"帮我", "查一下", "记一下", "算", "搜", "找"}

// ===== ShortInputCheckStep =====

// ShortInputCheckStep 判定短输入是否应跳过记忆检索
type ShortInputCheckStep struct{}

func NewShortInputCheckStep() *ShortInputCheckStep { return &ShortInputCheckStep{} }

func (s *ShortInputCheckStep) Name() string                       { return "shortInputCheck" }
func (s *ShortInputCheckStep) Requires() []string                 { return []string{"raw_msg"} }
func (s *ShortInputCheckStep) Produces() []string                 { return []string{"skip_memory", "is_short"} }
func (s *ShortInputCheckStep) ShouldRun(ctx context.Context) bool { return true }

func (s *ShortInputCheckStep) Run(ctx context.Context) (context.Context, error) {
	pc := GetPreContext(ctx)
	msg := pc.RawMsg
	charLen := utf8.RuneCountInString(msg)
	byteLen := len(msg)

	isShort := charLen <= 5 || (charLen <= 10 && byteLen <= 15)
	isQuestion := strings.Contains(msg, "?") || strings.Contains(msg, "？")
	hasIntent := false
	for _, kw := range intentKeywords {
		if strings.Contains(msg, kw) {
			hasIntent = true
			break
		}
	}

	skipMemory := false
	if isShort && !isQuestion && !hasIntent {
		skipMemory = true
		log.Printf("[pipeline] shortInputCheck: skip_memory=true (chars=%d, bytes=%d, question=%v, intent=%v)", charLen, byteLen, isQuestion, hasIntent)
	} else {
		log.Printf("[pipeline] shortInputCheck: skip_memory=false (chars=%d, bytes=%d, question=%v, intent=%v)", charLen, byteLen, isQuestion, hasIntent)
	}

	pc.SkipMemory = skipMemory
	pc.IsShort = isShort
	return ctx, nil
}

// ===== NormalizeDatesStep =====

// NormalizeDatesStep 将中文相对日期替换为绝对日期
type NormalizeDatesStep struct {
	now time.Time
}

func NewNormalizeDatesStep() *NormalizeDatesStep {
	return &NormalizeDatesStep{now: time.Now()}
}

func (s *NormalizeDatesStep) Name() string                       { return "normalizeDates" }
func (s *NormalizeDatesStep) Requires() []string                 { return []string{"raw_msg"} }
func (s *NormalizeDatesStep) Produces() []string                 { return []string{"normalized_msg"} }
func (s *NormalizeDatesStep) ShouldRun(ctx context.Context) bool { return true }

func (s *NormalizeDatesStep) Run(ctx context.Context) (context.Context, error) {
	pc := GetPreContext(ctx)
	normalized := pc.RawMsg

	weekdayNames := map[string]time.Weekday{
		"一": time.Monday, "二": time.Tuesday, "三": time.Wednesday,
		"四": time.Thursday, "五": time.Friday, "六": time.Saturday, "日": time.Sunday, "天": time.Sunday,
	}

	now := s.now.In(config.BJ)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, config.BJ)
	dateFmt := "2006-01-02"

	// 具体星期: 上周一、这周三、下周五
	for prefix, weekOffset := range map[string]int{"上": -1, "这": 0, "下": 1} {
		for name, wd := range weekdayNames {
			pattern := prefix + "周" + name
			daysUntil := int(wd) - int(today.Weekday())
			if weekOffset != 0 {
				daysUntil += weekOffset * 7
			}
			target := today.AddDate(0, 0, daysUntil)
			normalized = strings.ReplaceAll(normalized, pattern, target.Format(dateFmt))
		}
	}

	// 相对天: N天前, N天后
	daysAgoRe := regexp.MustCompile(`(\d+)天前`)
	normalized = daysAgoRe.ReplaceAllStringFunc(normalized, func(m string) string {
		nStr := daysAgoRe.FindStringSubmatch(m)[1]
		n, _ := strconv.Atoi(nStr)
		return today.AddDate(0, 0, -n).Format(dateFmt)
	})
	daysLaterRe := regexp.MustCompile(`(\d+)天后`)
	normalized = daysLaterRe.ReplaceAllStringFunc(normalized, func(m string) string {
		nStr := daysLaterRe.FindStringSubmatch(m)[1]
		n, _ := strconv.Atoi(nStr)
		return today.AddDate(0, 0, n).Format(dateFmt)
	})

	// 固定替换
	fixed := map[string]string{
		"前天": today.AddDate(0, 0, -2).Format(dateFmt),
		"后天": today.AddDate(0, 0, 2).Format(dateFmt),
		"昨天": today.AddDate(0, 0, -1).Format(dateFmt),
		"明天": today.AddDate(0, 0, 1).Format(dateFmt),
		"今天": today.Format(dateFmt),
	}
	for word, repl := range fixed {
		normalized = strings.ReplaceAll(normalized, word, repl)
	}

	if normalized != pc.RawMsg {
		log.Printf("[pipeline] normalizeDates: replaced relative dates in message")
	}

	pc.NormalizedMsg = normalized
	return ctx, nil
}

// ===== AutoRouteStep =====

// AgentLister — 获取用户智能体列表的接口（方便测试）
type AgentLister interface {
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Agent, int64, error)
}

// MessageCounter — 统计消息数量的接口
type MessageCounter interface {
	CountByRole(ctx context.Context, convID, role string) (int, error)
}

// AgentBinder — 绑定 agent 到会话（竞态保护）
type AgentBinder interface {
	SetAgentID(ctx context.Context, convID, agentID string) error
}

// AutoRouteStep 异步 LLM 路由匹配最合适的智能体
type AutoRouteStep struct {
	flashClient    llm.LLMClient
	agentRepo      AgentLister
	currentAgentID *string
	convID         string
	msgCounter     MessageCounter
	triggers       []int       // 触发路由的消息计数点，如 [1,3,6]
	agentBinder    AgentBinder // 竞态安全写入 DB
}

func NewAutoRouteStep(flashClient llm.LLMClient, agentRepo AgentLister, currentAgentID *string, convID string, msgCounter MessageCounter, triggers []int, agentBinder AgentBinder) *AutoRouteStep {
	return &AutoRouteStep{
		flashClient:    flashClient,
		agentRepo:      agentRepo,
		currentAgentID: currentAgentID,
		convID:         convID,
		msgCounter:     msgCounter,
		triggers:       triggers,
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
	if s.currentAgentID != nil && *s.currentAgentID != "" {
		return false
	}
	if len(s.triggers) == 0 || s.convID == "" || s.msgCounter == nil {
		return false
	}
	count, err := s.msgCounter.CountByRole(ctx, s.convID, "user")
	if err != nil {
		log.Printf("[pipeline] autoRoute: count user messages error: %v", err)
		return false
	}
	for _, t := range s.triggers {
		if count+1 == t {
			return true
		}
	}
	return false
}

// Run 异步路由——启动 goroutine，不阻塞管线
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
				if err := s.agentBinder.SetAgentID(bgCtx, convID, a.ID); err != nil {
					log.Printf("[pipeline] autoRoute(async): SetAgentID error: %v", err)
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

// ===== 工具函数 =====

// extractJSON 从混合文本中提取 JSON 对象
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// truncateSummary 截断文本到指定长度
func truncateSummary(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + "..."
}
