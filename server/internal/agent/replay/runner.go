package replay

import (
	"context"
	"encoding/json"
	"log"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/promptbuilder"
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

	// 检索参数覆盖（0 值表示使用默认）
	MemoryThreshold  float64 `json:"memory_threshold"`
	MemoryLimit      int     `json:"memory_limit"`
	NoteThreshold    float64 `json:"note_threshold"`
	NoteLimit        int     `json:"note_limit"`
	HistoryThreshold float64 `json:"history_threshold"`
	HistoryLimit     int     `json:"history_limit"`

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
}

// PreviewResult — 预览结果，包含完整 system prompt、消息列表和检索 trace
type PreviewResult struct {
	SystemPrompt string                     `json:"system_prompt"`
	Messages     []llm.Message              `json:"messages"`
	Trace        *promptbuilder.PromptTrace `json:"trace,omitempty"`
}

// Preview 构建 system prompt 并返回完整的请求预览
func (r *Runner) Preview(ctx context.Context, anchorMsg *model.Message, history []model.Message, cfg *Config) (*PreviewResult, error) {
	msgs := agent.BuildMessagesForFlash(history, cfg.MaxToolResultChars)
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
		UserID:           anchorMsg.UserID,
		ConversationID:   anchorMsg.ConversationID,
		UserMessage:      anchorMsg.Content,
		Embedding:        vec,
		MemoryThreshold:  cfg.MemoryThreshold,
		MemoryLimit:      cfg.MemoryLimit,
		NoteThreshold:    cfg.NoteThreshold,
		NoteLimit:        cfg.NoteLimit,
		HistoryThreshold: cfg.HistoryThreshold,
		HistoryLimit:     cfg.HistoryLimit,
	})
	if err != nil {
		log.Printf("[replay] preview build error: %v", err)
		return &PreviewResult{Messages: msgs}, nil
	}

	return &PreviewResult{
		SystemPrompt: result.Prompt,
		Messages:     msgs,
		Trace:        result.Trace,
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

	msgs := agent.BuildMessagesForFlash(history, cfg.MaxToolResultChars)

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
		MemoryThreshold:      cfg.MemoryThreshold,
		MemoryLimit:          cfg.MemoryLimit,
		NoteThreshold:        cfg.NoteThreshold,
		NoteLimit:            cfg.NoteLimit,
		HistoryThreshold:     cfg.HistoryThreshold,
		HistoryLimit:         cfg.HistoryLimit,
	})
	if err != nil {
		log.Printf("[replay] orch run error: %v", err)
		return nil, err
	}

	result := &Result{ConversationID: convID}
	var currentToolCall *CapturedToolCall

	for evt := range events {
		switch evt.Type {
		case "tool_call":
			var tc tool.ToolCall
			if err := json.Unmarshal([]byte(evt.Content), &tc); err == nil {
				currentToolCall = &CapturedToolCall{
					Tool:     tc.Tool,
					Function: tc.Function,
					Args:     tc.Args,
				}
				result.ToolCalls = append(result.ToolCalls, *currentToolCall)
			}
		case "tool_result":
			if currentToolCall != nil {
				idx := len(result.ToolCalls) - 1
				if idx >= 0 {
					result.ToolCalls[idx].Result = json.RawMessage(evt.Content)
				}
			}
			currentToolCall = nil
		case "text_delta":
			result.Reply += evt.Content
		case "done":
			// 最终回复已在 text_delta 中累积
		case "error":
			log.Printf("[replay] error event: %s", evt.Error)
			return result, nil
		}
	}

	return result, nil
}
