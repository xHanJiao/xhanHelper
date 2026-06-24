package replay

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/agent"
	"xhan-helper/internal/agent/ctxflow"
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
	Summary retrieval.SourceConfig `json:"summary"`

	OverrideSystemPrompt *string `json:"override_system_prompt,omitempty"`
}

// Runner 重放执行器，使用独立 SQLite 存储重放消息，复用主应用的 LLM/Tool/检索
type Runner struct {
	flashClient        llm.LLMClient
	proClient          llm.LLMClient
	registry           *tool.Registry
	promptBuilder      *promptbuilder.PromptBuilder
	embedder           *llm.Embedder
	contextRetriever   *ctxflow.ContextRetriever
	contextFormatter   ctxflow.ContextFormatter
	maxIterations      int
	maxTokens          *int
	toolMaxConcurrency int
	useNativeFC        bool
}

// RunnerConfig — Runner 依赖
type RunnerConfig struct {
	FlashClient        llm.LLMClient
	ProClient          llm.LLMClient
	Registry           *tool.Registry
	PromptBuilder      *promptbuilder.PromptBuilder
	Embedder           *llm.Embedder
	ContextRetriever   *ctxflow.ContextRetriever
	ContextFormatter   ctxflow.ContextFormatter
	MaxIterations      int
	MaxTokens          *int
	ToolMaxConcurrency int
	UseNativeFC        bool
}

// NewRunner 创建重放执行器
func NewRunner(cfg *RunnerConfig) *Runner {
	return &Runner{
		flashClient:        cfg.FlashClient,
		proClient:          cfg.ProClient,
		registry:           cfg.Registry,
		promptBuilder:      cfg.PromptBuilder,
		embedder:           cfg.Embedder,
		contextRetriever:   cfg.ContextRetriever,
		contextFormatter:   cfg.ContextFormatter,
		maxIterations:      cfg.MaxIterations,
		maxTokens:          cfg.MaxTokens,
		toolMaxConcurrency: cfg.ToolMaxConcurrency,
		useNativeFC:        cfg.UseNativeFC,
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
	ConversationID string                 `json:"conversation_id"`
	ToolCalls      []CapturedToolCall     `json:"tool_calls"`
	Reply          string                 `json:"reply"`
	Reasoning      []string               `json:"reasoning"`
	SystemPrompt   string                 `json:"system_prompt,omitempty"`
	RetrievalTrace *ctxflow.RetrieveTrace `json:"retrieval_trace,omitempty"`
}

// PreviewResult — 预览结果
type PreviewResult struct {
	SystemPrompt      string                 `json:"system_prompt"`
	UserMessagePrefix string                 `json:"user_message_prefix"`
	Messages          []llm.Message          `json:"messages"`
	Trace             *ctxflow.RetrieveTrace `json:"trace,omitempty"`
}

// Preview 构建 system prompt 并返回完整的请求预览
func (r *Runner) Preview(ctx context.Context, anchorMsg *model.Message, history []model.Message, cfg *Config) (*PreviewResult, error) {
	msgs := agent.BuildMessagesForFlash(history, cfg.MaxToolResultChars, false)
	msgs = append(msgs, llm.Message{Role: "user", Content: anchorMsg.Content})

	// 1. 静态 system prompt
	pbResult, err := r.promptBuilder.Build(ctx, promptbuilder.BuildOptions{
		UserID:      anchorMsg.UserID,
		AgentPrompt: "",
	})
	if err != nil {
		log.Printf("[replay] preview build error: %v", err)
		return &PreviewResult{Messages: msgs}, nil
	}

	// 2. 上下文检索
	var retrieveResult *ctxflow.RetrieveResult
	var userMsgPrefix string
	if !cfg.SkipRetrieval && r.embedder != nil && r.contextRetriever != nil {
		vec, err := r.embedder.Embed(ctx, anchorMsg.Content)
		if err != nil {
			log.Printf("[replay] preview embed error: %v", err)
		} else {
			retrieveResult, err = r.contextRetriever.Retrieve(ctx, retrieval.SearchOptions{
				UserID:         anchorMsg.UserID,
				ConversationID: anchorMsg.ConversationID,
				Query:          anchorMsg.Content,
				Embedding:      vec,
				Memory:         cfg.Memory,
				Note:           cfg.Note,
				History:        cfg.History,
				Theme:          cfg.Theme,
				Summary:        cfg.Summary,
			})
			if err != nil {
				log.Printf("[replay] preview retrieve error: %v", err)
			}
		}
		if r.contextFormatter != nil && retrieveResult != nil {
			extraBlocks := []ctxflow.ExtraBlock{ctxflow.DateTimeBlock(anchorMsg.CreatedAt)}
			userMsgPrefix = r.contextFormatter.FormatBlocks(retrieveResult, extraBlocks...)
		}
	}

	var trace *ctxflow.RetrieveTrace
	if retrieveResult != nil {
		trace = retrieveResult.Trace
	}

	return &PreviewResult{
		SystemPrompt:      pbResult.Prompt,
		UserMessagePrefix: userMsgPrefix,
		Messages:          msgs,
		Trace:             trace,
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

	msgs := agent.BuildMessagesForFlash(history, cfg.MaxToolResultChars, false)

	temperature := cfg.Temperature
	maxToolResultChars := cfg.MaxToolResultChars
	if maxToolResultChars <= 0 {
		maxToolResultChars = 2000
	}

	var trace *ctxflow.RetrieveTrace
	prebuiltContent := anchorMsg.Content

	if !cfg.SkipRetrieval {
		var vec []float64
		if r.embedder != nil {
			v, err := r.embedder.Embed(ctx, anchorMsg.Content)
			if err != nil {
				log.Printf("[replay] embed error: %v", err)
			} else {
				vec = v
			}
		}
		retrieveResult, err := r.contextRetriever.Retrieve(ctx, retrieval.SearchOptions{
			UserID:         anchorMsg.UserID,
			ConversationID: anchorMsg.ConversationID,
			Query:          anchorMsg.Content,
			Embedding:      vec,
			Memory:         cfg.Memory,
			Note:           cfg.Note,
			History:        cfg.History,
			Theme:          cfg.Theme,
			Summary:        cfg.Summary,
		})
		if err != nil {
			log.Printf("[replay] retrieve error: %v", err)
		} else {
			trace = retrieveResult.Trace
			extraBlocks := []ctxflow.ExtraBlock{ctxflow.DateTimeBlock(anchorMsg.CreatedAt)}
			prebuiltContent = r.contextFormatter.FormatUserMessage(retrieveResult, anchorMsg.Content, extraBlocks...)
		}
	}

	// 构建 system prompt（仅用于 Result 展示）
	pbResult, _ := r.promptBuilder.Build(ctx, promptbuilder.BuildOptions{UserID: anchorMsg.UserID})
	var systemPromptForDisplay string
	if pbResult != nil {
		systemPromptForDisplay = pbResult.Prompt
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
		ToolMaxConcurrency: r.toolMaxConcurrency,
		OnChatEnd:          nil,
		UseNativeFC:        r.useNativeFC,
		ContextRetriever:   r.contextRetriever,
		ContextFormatter:   r.contextFormatter,
	})

	events, err := orch.Run(ctx, &agent.RunRequest{
		UserID:             anchorMsg.UserID,
		ConversationID:     convID,
		Messages:           msgs,
		RawUserMessage:     anchorMsg.Content,
		SkipRetrieval:      true, // 已在上面预构建
		UserMessageContent: prebuiltContent,
		MaxIterations:      cfg.MaxIterations,
		Temperature:        temperature,
		MaxTokens:          r.maxTokens,
		Memory:             cfg.Memory,
		Note:               cfg.Note,
		History:            cfg.History,
		Theme:              cfg.Theme,
	})
	if err != nil {
		log.Printf("[replay] orch run error: %v", err)
		return nil, err
	}

	result := r.collectEvents(convID, events)
	result.RetrievalTrace = trace
	result.SystemPrompt = systemPromptForDisplay
	return result, nil
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
			result.Reasoning = append(result.Reasoning, curReasoning.String())
			curReasoning.Reset()
		case "error":
			log.Printf("[replay] error event: %s", evt.Error)
			return result
		}
	}
	if curReasoning.Len() > 0 {
		result.Reasoning = append(result.Reasoning, curReasoning.String())
	}
	return result
}
