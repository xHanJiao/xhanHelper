package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/promptbuilder"
	"xhan-helper/internal/agent/retrieval"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
	"xhan-helper/internal/tool"
)

// Config — 一次重放运行的配置覆盖
type Config struct {
	SkipRetrieval      bool     `json:"skip_retrieval"`
	MaxToolResultChars int      `json:"max_tool_result_chars"`
	DeepMode           bool     `json:"deep_mode"`
	MaxIterations      int      `json:"max_iterations"`
	Temperature        *float64 `json:"temperature,omitempty"`

	Memory  retrieval.SourceConfig `json:"memory"`
	Note    retrieval.SourceConfig `json:"note"`
	History retrieval.SourceConfig `json:"history"`
	Theme   retrieval.SourceConfig `json:"theme"`

	// 手动覆盖完整 system prompt（nil=自动构建，非 nil 直接使用，含空串）
	OverrideSystemPrompt *string `json:"override_system_prompt,omitempty"`
}

// Runner 重放执行器，使用独立 SQLite 存储重放消息，复用主应用的 LLM/Tool/检索
type Runner struct {
	flashClient   llm.LLMClient
	proClient     llm.LLMClient
	registry      *tool.Registry
	promptBuilder *promptbuilder.SystemPromptBuilder
	embedder      *llm.Embedder
	maxIterations int
	maxTokens     *int
}

// RunnerConfig — Runner 依赖
type RunnerConfig struct {
	FlashClient   llm.LLMClient
	ProClient     llm.LLMClient
	Registry      *tool.Registry
	PromptBuilder *promptbuilder.SystemPromptBuilder
	Embedder      *llm.Embedder
	MaxIterations int
	MaxTokens     *int
}

// NewRunner 创建重放执行器
func NewRunner(cfg *RunnerConfig) *Runner {
	return &Runner{
		flashClient:   cfg.FlashClient,
		proClient:     cfg.ProClient,
		registry:      cfg.Registry,
		promptBuilder: cfg.PromptBuilder,
		embedder:      cfg.Embedder,
		maxIterations: cfg.MaxIterations,
		maxTokens:     cfg.MaxTokens,
	}
}

// CapturedToolCall — 捕获的工具调用记录
type CapturedToolCall struct {
	Tool     string          `json:"tool"`
	Function string          `json:"function"`
	Args     json.RawMessage `json:"args"`
	Result   json.RawMessage `json:"result"`
}

// Result — 一次重放的完整结果
type Result struct {
	ConversationID string             `json:"conversation_id"`
	ToolCalls      []CapturedToolCall `json:"tool_calls"`
	Reply          string             `json:"reply"`
	Reasoning      []string           `json:"reasoning"`
}

// PreviewResult — 预览结果，包含完整 system prompt、消息列表和检索 trace
type PreviewResult struct {
	SystemPrompt      string                     `json:"system_prompt"`
	UserMessagePrefix string                     `json:"user_message_prefix"`
	Messages          []llm.Message              `json:"messages"`
	Trace             *promptbuilder.PromptTrace `json:"trace,omitempty"`
}

// Preview 构建 system prompt 并返回完整的请求预览
func (r *Runner) Preview(ctx context.Context, anchorMsg *model.Message, history []model.Message, cfg *Config) (*PreviewResult, error) {
	msgs := agent.BuildMessagesForFlash(history, cfg.MaxToolResultChars, false) // replay 走旧格式
	msgs = append(msgs, llm.Message{Role: "user", Content: anchorMsg.Content})

	var vec []float64
	if !cfg.SkipRetrieval && r.embedder != nil {
		v, err := r.embedder.Embed(ctx, anchorMsg.Content)
		if err != nil {
			log.Printf("[replay] preview embed error: %v", err)
		} else {
			vec = v
		}
	}

	result, err := r.promptBuilder.Build(ctx, promptbuilder.BuildOptions{
		UserID:         anchorMsg.UserID,
		ConversationID: anchorMsg.ConversationID,
		UserMessage:    anchorMsg.Content,
		Embedding:      vec,
		Memory:         cfg.Memory,
		Note:           cfg.Note,
		History:        cfg.History,
		Theme:          cfg.Theme,
	})
	if err != nil {
		log.Printf("[replay] preview build error: %v", err)
		return &PreviewResult{Messages: msgs}, nil
	}

	return &PreviewResult{
		SystemPrompt:      result.Prompt,
		UserMessagePrefix: result.UserMessagePrefix,
		Messages:          msgs,
		Trace:             result.Trace,
	}, nil
}

// Run 执行一次重放，所有消息写入独立的 SQLite 内存库
func (r *Runner) Run(ctx context.Context, anchorMsg *model.Message, history []model.Message, cfg *Config) (*Result, error) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.Message{}); err != nil {
		return nil, err
	}

	msgRepo := service.NewMessageRepo(db)
	convRepo := service.NewConversationRepo(db)

	convID := uuid.New().String()
	if err := convRepo.Create(ctx, &model.Conversation{
		ID:     convID,
		UserID: anchorMsg.UserID,
		Title:  "replay-" + anchorMsg.ID[:8],
	}); err != nil {
		return nil, err
	}

	msgs := agent.BuildMessagesForFlash(history, cfg.MaxToolResultChars, false) // replay 走旧格式

	temperature := cfg.Temperature
	maxToolResultChars := cfg.MaxToolResultChars
	if maxToolResultChars <= 0 {
		maxToolResultChars = 2000
	}

	orch := agent.New(&agent.OrchestratorConfig{
		FlashClient:        r.flashClient,
		ProClient:          r.proClient,
		Registry:           r.registry,
		PromptBuilder:      r.promptBuilder,
		MsgRepo:            msgRepo,
		ConvRepo:           convRepo,
		Embedder:           r.embedder,
		VectorStore:        nil,
		EmbeddingRepo:      nil,
		MaxIterations:      r.maxIterations,
		Temperature:        temperature,
		MaxTokens:          r.maxTokens,
		MaxToolResultChars: maxToolResultChars,
		OnChatEnd:          nil,
	})

	events, err := orch.Run(ctx, &agent.RunRequest{
		UserID:               anchorMsg.UserID,
		ConversationID:       convID,
		Messages:             msgs,
		UserMessageContent:   anchorMsg.Content,
		RawUserMessage:       anchorMsg.Content,
		SkipRetrieval:        cfg.SkipRetrieval,
		OverrideSystemPrompt: cfg.OverrideSystemPrompt,
		MaxIterations:        cfg.MaxIterations,
		Temperature:          temperature,
		MaxTokens:            r.maxTokens,
		Memory:               cfg.Memory,
		Note:                 cfg.Note,
		History:              cfg.History,
		Theme:                cfg.Theme,
	})
	if err != nil {
		log.Printf("[replay] orch run error: %v", err)
		return nil, err
	}

	return r.collectEvents(convID, events), nil
}

// RunFromRequestJSON 用历史 request_json 的 system prompt 走完整 ReAct 循环
func (r *Runner) RunFromRequestJSON(ctx context.Context, anchorMsg *model.Message, history []model.Message, requestBody json.RawMessage) (*Result, error) {
	// 1. 解析 request_json，只提取 system prompt 和 temperature
	var apiReq struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Temperature *float64 `json:"temperature"`
	}
	if err := json.Unmarshal(requestBody, &apiReq); err != nil {
		return nil, fmt.Errorf("parse request_json: %w", err)
	}

	var systemPrompt string
	for _, m := range apiReq.Messages {
		if m.Role == "system" {
			systemPrompt = m.Content
			break
		}
	}
	if systemPrompt == "" {
		return nil, fmt.Errorf("no system message found in request_json")
	}

	// 2. 创建隔离的 SQLite 库 + orchestrator
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.Message{}); err != nil {
		return nil, err
	}

	msgRepo := service.NewMessageRepo(db)
	convRepo := service.NewConversationRepo(db)

	convID := uuid.New().String()
	if err := convRepo.Create(ctx, &model.Conversation{
		ID:     convID,
		UserID: anchorMsg.UserID,
		Title:  "replay-raw-" + convID[:8],
	}); err != nil {
		return nil, err
	}

	maxToolResultChars := 2000
	msgs := agent.BuildMessagesForFlash(history, maxToolResultChars, false) // replay 走旧格式

	overridePrompt := systemPrompt
	orch := agent.New(&agent.OrchestratorConfig{
		FlashClient:        r.flashClient,
		ProClient:          r.proClient,
		Registry:           r.registry,
		PromptBuilder:      r.promptBuilder,
		MsgRepo:            msgRepo,
		ConvRepo:           convRepo,
		Embedder:           r.embedder,
		VectorStore:        nil,
		EmbeddingRepo:      nil,
		MaxIterations:      r.maxIterations,
		Temperature:        apiReq.Temperature,
		MaxTokens:          r.maxTokens,
		MaxToolResultChars: maxToolResultChars,
		OnChatEnd:          nil,
	})

	events, err := orch.Run(ctx, &agent.RunRequest{
		UserID:               anchorMsg.UserID,
		ConversationID:       convID,
		Messages:             msgs,
		UserMessageContent:   anchorMsg.Content,
		RawUserMessage:       anchorMsg.Content,
		SkipRetrieval:        true,
		OverrideSystemPrompt: &overridePrompt,
		MaxIterations:        r.maxIterations,
	})
	if err != nil {
		log.Printf("[replay] raw orch run error: %v", err)
		return nil, err
	}

	return r.collectEvents(convID, events), nil
}

// collectEvents 从事件通道收集工具调用和最终回复
func (r *Runner) collectEvents(convID string, events <-chan *llm.StreamEvent) *Result {
	result := &Result{ConversationID: convID}
	var curReasoning strings.Builder
	for evt := range events {
		switch evt.Type {
		case "tool_call":
			var tc tool.ToolCall
			if err := json.Unmarshal([]byte(evt.Content), &tc); err == nil {
				result.ToolCalls = append(result.ToolCalls, CapturedToolCall{
					Tool:     tc.Tool,
					Function: tc.Function,
					Args:     tc.Args,
				})
			}
		case "tool_result":
			for i := range result.ToolCalls {
				if len(result.ToolCalls[i].Result) == 0 {
					result.ToolCalls[i].Result = json.RawMessage(evt.Content)
					break
				}
			}
		case "text_delta":
			result.Reply += evt.Content
		case "thought_delta":
			curReasoning.WriteString(evt.Content)
		case "thought_done":
			// thought_done 标志一轮思考结束，压入切片，开始新的一轮
			result.Reasoning = append(result.Reasoning, curReasoning.String())
			curReasoning.Reset()
		case "error":
			log.Printf("[replay] error event: %s", evt.Error)
			return result
		}
	}
	// 兜底：如果最后一轮思考没有 thought_done 事件（异常情况），不丢失内容
	if curReasoning.Len() > 0 {
		result.Reasoning = append(result.Reasoning, curReasoning.String())
	}
	return result
}
