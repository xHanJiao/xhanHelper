package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/llmcost"
)

// maxImageDescriptionLen 图片描述长度上限（字），与 ImageAsset.Description 列宽一致。
const maxImageDescriptionLen = 500

const imageDescribeSystemPrompt = "你是图片描述助手。只输出对图片内容的简短客观描述，不要客套、不要解释、不要 Markdown 格式。"

const imageDescribeUserPrompt = "用一到两句中文描述这张图片的内容，突出可用于检索的关键信息（主体、场景、画面中的文字要点）。直接输出描述本身。"

// ImageDescriber — 用多模态模型把图片转成一句简短文字描述。
// 目的是让 LLM 在图片列表里按文字选图，避免每次都识图烧 token。
// 由 agent 实现 service.ImageDescriber，注入 ImageStore。
type ImageDescriber struct {
	client llm.LLMClient
}

// NewImageDescriber 创建描述生成器。client 为 nil 直接 panic（接线 bug）。
func NewImageDescriber(client llm.LLMClient) *ImageDescriber {
	if client == nil {
		panic("image_describer: nil llm client (wiring bug)")
	}
	return &ImageDescriber{client: client}
}

// Describe 用 file_id 让模型看图并输出描述。图片只能出现在 user 消息（走 file 块）。
func (d *ImageDescriber) Describe(ctx context.Context, fileID string) (string, error) {
	if fileID == "" {
		return "", fmt.Errorf("describe image: empty file id")
	}
	ctx = llmcost.WithScenario(ctx, llmcost.Scenario{
		Category:    llmcost.CategoryPostChat,
		SubCategory: llmcost.SubImageDescribe,
	})
	msg, err := d.client.ChatSync(ctx, &llm.ChatRequest{
		SystemPrompt: imageDescribeSystemPrompt,
		Messages: []llm.Message{{
			Role:    "user",
			Content: imageDescribeUserPrompt,
			Images:  []llm.ImageRef{{FileID: fileID}},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("describe image: %w", err)
	}
	desc := strings.TrimSpace(msg.Content)
	if desc == "" {
		return "", fmt.Errorf("describe image: empty response")
	}
	if utf8.RuneCountInString(desc) > maxImageDescriptionLen {
		desc = string([]rune(desc)[:maxImageDescriptionLen])
	}
	return desc, nil
}
