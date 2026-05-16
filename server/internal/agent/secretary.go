package agent

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

const secretaryPrompt = `你是 xhanHelper 的个人秘书。你将收到分析系统产出的「用户数据发现」，你需要为每条发现产出两份文案。

## 两份文案的定位

1. **notification（站内信/推送）**：极简，1-2 句话，50 字以内。像推送通知，不说废话
2. **greeting（对话开场白）**：可以稍长，3-5 句话。像一个关心你的人在开启对话——提到发现了什么、问一个问题邀请你聊下去、营造继续对话的感觉

## 风格要求

- 像一个人跟你说话，不像一份报告
- 用「你」而非「用户」
- 语气：平和、关心但不煽情。说事实，不要打气

## 输入

每条 finding 含 title、content、suggested_action、theme_name。

## 输出

{"messages":[{"title":"推送标题","notification":"推送正文50字内","greeting":"对话开场白，3-5句话，吸引继续聊下去"}]}`

// Secretary — 秘书层，把分析发现转化为人性化提醒
type Secretary struct {
	flashClient llm.LLMClient
}

func NewSecretary(flashClient llm.LLMClient) *Secretary {
	return &Secretary{flashClient: flashClient}
}

func (s *Secretary) Name() string        { return "secretary" }
func (s *Secretary) Description() string { return "把分析发现转化为友好的人性化提醒" }

type secretaryArgs struct {
	Findings []json.RawMessage `json:"findings"`
}

func (s *Secretary) Functions() []tool.FuncSchema {
	return []tool.FuncSchema{{
		Name:        "rewrite",
		Description: "将 scorer 返回的 TopN 发现转化为友好的人性化提醒消息和对话开场白",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"findings": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"type": "object"},
				},
			},
			"required": []string{"findings"},
		},
	}}
}

func (s *Secretary) Execute(ec *tool.ExecuteContext, funcName string, args json.RawMessage) *tool.Result {
	var a secretaryArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return &tool.Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
	}
	if len(a.Findings) == 0 {
		return &tool.Result{Success: false, Error: "findings 不能为空"}
	}

	findingsJSON, _ := json.Marshal(a.Findings)
	userMsg := fmt.Sprintf("请将以下分析发现转化：\n\n%s", string(findingsJSON))

	resp, err := s.flashClient.ChatSync(ec.Ctx, &llm.ChatRequest{
		SystemPrompt: secretaryPrompt,
		Messages:     []llm.Message{{Role: "user", Content: userMsg}},
		UserID:       ec.UserID,
	})
	if err != nil {
		log.Printf("[secretary] ChatSync error: %v", err)
		return &tool.Result{Success: false, Error: fmt.Sprintf("秘书转化失败: %v", err)}
	}

	// 写 trace
	if traceRepo := CtxTraceRepo(ec.Ctx); traceRepo != nil {
		var reqJSON, respJSON string
		if resp.RequestJSON != nil {
			reqJSON = *resp.RequestJSON
		}
		if resp.ResponseJSON != nil {
			respJSON = *resp.ResponseJSON
		}
		pt, ct := extractUsage(respJSON)
		traceRepo.CreateLLMCall(ec.Ctx, &model.ReflectionLLMCall{
			ID:               uuid.New().String(),
			TraceID:          CtxTraceID(ec.Ctx),
			Caller:           "Secretary",
			RequestJSON:      &reqJSON,
			ResponseJSON:     &respJSON,
			PromptTokens:     pt,
			CompletionTokens: ct,
		})
	}

	var output struct {
		Messages []struct {
			Title        string `json:"title"`
			Notification string `json:"notification"`
			Greeting     string `json:"greeting"`
		} `json:"messages"`
	}
	if _, parseErr := ParseFlashJSON(resp.Content, &output); parseErr != nil {
		log.Printf("[secretary] parse error: %v", parseErr)
		return &tool.Result{Success: false, Error: fmt.Sprintf("解析秘书结果失败: %v", parseErr)}
	}

	data, _ := json.Marshal(output)
	log.Printf("[secretary] rewritten %d messages", len(output.Messages))
	return &tool.Result{Success: true, Data: data}
}
