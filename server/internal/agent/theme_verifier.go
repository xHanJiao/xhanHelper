package agent

import (
	"context"
	"log"
	"strings"

	"xhan-helper/internal/llm"
)

var stateVerifyPrompt = `你是一个状态页检验助手。对比旧状态和原始数据，判断新提出的状态页是否准确。

## 旧状态
%s

## 原始数据
%s

## 新状态（待检验）
%s

## 检验规则
1. 无幻觉：新状态的每一条陈述都必须能在旧状态或原始数据中找到依据，不能编造不存在的事实
2. 无丢失：旧状态中仍然有效的关键信息不能无故消失。如果新旧状态都未提及某信息，不算丢失
3. 不冗余：不超过 500 字

## 输出（纯 JSON，无其他文字）
{"complete": true/false, "hallucinations": ["编造的陈述"], "missing": ["丢失的关键信息"], "suggestion": "改进建议"}

## 通过检验的例子
- 旧状态说"投递了3家公司"，原始数据有一条新面试邀约，新状态合并为"已投递3家公司，收到1家面试邀约" → 通过
- 旧状态有条规律"A导致B"，原始数据出现反例"A发生了但B没有"，新状态删除了这条规律 → 通过（不以旧状态为准，旧状态的信息可以被新数据推翻）

## 不通过检验的例子
- 原始数据只说"面试了一次"，新状态写"已完成两轮面试" → 幻觉
- 旧状态有"不加班是底线"，新状态全文未提及 → 关键信息丢失
- 新状态 800 字 → 冗余`

// VerifyResult 检验结果
type VerifyResult struct {
	Complete       bool     `json:"complete"`
	Hallucinations []string `json:"hallucinations"`
	Missing        []string `json:"missing"`
	Suggestion     string   `json:"suggestion"`
}

// VerifyStateUpdate 检验提出的 state_text 是否准确完整。
// sourceText 对 StateUpdater 是对话文本，对 ThemeAnalyst 是检索结果。
func VerifyStateUpdate(ctx context.Context, flashClient llm.LLMClient, userID string, oldState, sourceText, proposed string) (*VerifyResult, error) {
	prompt := formatVerifyPrompt(oldState, sourceText, proposed)

	resp, err := flashClient.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: prompt,
		Messages:     []llm.Message{{Role: "user", Content: "检验状态页"}},
		UserID:       userID,
	})
	if err != nil {
		log.Printf("[verify_state] ChatSync error: %v", err)
		return nil, err
	}

	var vr VerifyResult
	if _, parseErr := ParseFlashJSON(resp.Content, &vr); parseErr != nil {
		log.Printf("[verify_state] parse error: %v, raw=%s", parseErr, safeTruncate(resp.Content, 300))
		return nil, parseErr
	}

	if vr.Complete {
		log.Printf("[verify_state] passed")
	} else {
		log.Printf("[verify_state] failed: hallucinations=%d missing=%d suggestion=%s",
			len(vr.Hallucinations), len(vr.Missing), safeTruncate(vr.Suggestion, 100))
	}
	return &vr, nil
}

func formatVerifyPrompt(oldState, sourceText, proposed string) string {
	s := strings.Replace(stateVerifyPrompt, "%s", safeTruncate(oldState, 1500), 1)
	s = strings.Replace(s, "%s", safeTruncate(sourceText, 8000), 1)
	s = strings.Replace(s, "%s", safeTruncate(proposed, 1500), 1)
	return s
}

func safeTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
