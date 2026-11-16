package llmcost

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// IOAudit — 一次 LLM 调用的审计内容（I/O 快照）。
type IOAudit struct {
	Category         string
	SubCategory      string
	UserID           string
	ConvID           string
	RefType          string
	RefID            string
	Model            string
	SystemPrompt     string
	MessagesJSON     string
	Response         string
	PromptTokens     int64
	CompletionTokens int64
	LatencyMs        int64
	ErrorMsg         string
}

// auditWhitelist — 审计场景白名单（category → sub_category）。
// reflection 不进入（该分类自 2026-11-15 起已无产出，仅存量成本数据），main_chat 不进入（已有 request_json）。
var auditWhitelist = map[string]map[string]bool{
	CategoryPostChat: {
		SubMemoryExtract:    true,
		SubEventAssemble:    true,
		SubProfileExtract:   true,
		SubProfileColdStart: true,
		SubSummary:          true,
	},
	CategoryAdmin: {SubAgentSummary: true},
	CategoryTool:  {SubReminderGen: true, SubRecordMemory: true},
}

// shouldAudit 判断是否记录审计。category 为空（未标注 scenario）→ uncategorized 兜底记录。
func shouldAudit(category, sub string) bool {
	if category == "" {
		return true // uncategorized 兜底，不丢调用
	}
	if subs, ok := auditWhitelist[category]; ok {
		return subs[sub]
	}
	return false
}

// IOAuditWrapper — LLM 客户端装饰器：ChatSync 捕获 I/O 写审计（异步，失败只打日志）。
// 流式 Chat 不捕获（主聊天不在审计范围，见 llm-call-audit.md）。
type IOAuditWrapper struct {
	inner llm.LLMClient
	repo  *service.LLMCallIORepo
}

// WrapIOAudit 包一层审计装饰器。repo 为 nil 时直接返回 inner（不审计）。
func WrapIOAudit(inner llm.LLMClient, repo *service.LLMCallIORepo) llm.LLMClient {
	if inner == nil || repo == nil {
		return inner
	}
	return &IOAuditWrapper{inner: inner, repo: repo}
}

func (w *IOAuditWrapper) ModelName() string { return w.inner.ModelName() }

func (w *IOAuditWrapper) BuildSystemPrompt(base string, tools []llm.ToolDefinition) string {
	return w.inner.BuildSystemPrompt(base, tools)
}

func (w *IOAuditWrapper) Chat(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamEvent, error) {
	return w.inner.Chat(ctx, req)
}

func (w *IOAuditWrapper) ChatWithRawJSON(ctx context.Context, requestBody json.RawMessage) (llm.Message, error) {
	return w.inner.ChatWithRawJSON(ctx, requestBody)
}

func (w *IOAuditWrapper) ChatSync(ctx context.Context, req *llm.ChatRequest) (llm.Message, error) {
	start := time.Now()
	msg, err := w.inner.ChatSync(ctx, req)
	scenario, _ := ScenarioFromContext(ctx)
	if !shouldAudit(scenario.Category, scenario.SubCategory) {
		return msg, err
	}
	messagesJSON, mErr := json.Marshal(req.Messages)
	if mErr != nil {
		log.Printf("[llm_audit] marshal messages error: %v", mErr)
		messagesJSON = nil
	}
	audit := IOAudit{
		Category:     scenario.Category,
		SubCategory:  scenario.SubCategory,
		UserID:       scenario.UserID,
		ConvID:       scenario.ConvID,
		RefType:      scenario.RefType,
		RefID:        scenario.RefID,
		Model:        w.inner.ModelName(),
		SystemPrompt: req.SystemPrompt,
		MessagesJSON: string(messagesJSON),
		LatencyMs:    time.Since(start).Milliseconds(),
	}
	if err != nil {
		audit.ErrorMsg = err.Error()
	} else {
		audit.Response = msg.Content
		if msg.Usage != nil {
			audit.PromptTokens = int64(msg.Usage.PromptTokens)
			audit.CompletionTokens = int64(msg.Usage.CompletionTokens)
		}
	}
	go w.persist(audit)
	return msg, err
}

// persist 异步落库。审计是旁路，失败只打日志，不重试。
func (w *IOAuditWrapper) persist(audit IOAudit) {
	row := &model.LLMCallIO{
		ID:               idgen.Next(),
		Category:         audit.Category,
		SubCategory:      audit.SubCategory,
		UserID:           audit.UserID,
		ConvID:           audit.ConvID,
		RefType:          audit.RefType,
		RefID:            audit.RefID,
		Model:            audit.Model,
		SystemPrompt:     audit.SystemPrompt,
		MessagesJSON:     audit.MessagesJSON,
		Response:         audit.Response,
		PromptTokens:     audit.PromptTokens,
		CompletionTokens: audit.CompletionTokens,
		LatencyMs:        audit.LatencyMs,
		ErrorMsg:         audit.ErrorMsg,
	}
	if err := w.repo.Create(context.Background(), row); err != nil {
		log.Printf("[llm_audit] insert error: %v", err)
	}
}
