package agent

import (
	"context"
	"strings"
	"testing"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/agent/retrieval"
)

// 文档引用（「问 AI」）只进 XMLContext，不进用户正文。
// 正文一旦被污染，检索/向量/记忆/摘要都会把"引用原文"当成用户说的话。
func TestBuildUserMessage_DocQuoteOnlyInXMLContext(t *testing.T) {
	o := &Orchestrator{contextFormatter: ctxflow.NewXMLFormatter()}
	req := &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		UserMessageContent: "这句话什么意思？",
		RawUserMessage:     "这句话什么意思？",
		DocQuote: &retrieval.DocQuoteMeta{
			DocumentID: "doc-1",
			Title:      "记忆系统调研",
			Quote:      "在渲染期把反思结果直接拼进 persona",
			Prefix:     "前文",
			Suffix:     "后文",
		},
	}

	msg := o.buildUserMessage(context.Background(), req, nil)

	if msg.XMLContext == nil {
		t.Fatal("XMLContext 未设置")
	}
	for _, want := range []string{
		"<document_quote",
		`document_id="doc-1"`,
		"<quote>在渲染期把反思结果直接拼进 persona</quote>",
		"<prefix>前文</prefix>",
		"<suffix>后文</suffix>",
	} {
		if !strings.Contains(*msg.XMLContext, want) {
			t.Errorf("XMLContext 缺少 %q: %s", want, *msg.XMLContext)
		}
	}
	if msg.Content != "这句话什么意思？" {
		t.Errorf("用户正文被污染: %q", msg.Content)
	}
	if msg.OriginalContent != nil {
		t.Errorf("不该写 OriginalContent: %q", *msg.OriginalContent)
	}
}

// 无引用时不产出空块（也不该多出任何 document_quote 痕迹）。
func TestBuildUserMessage_NoDocQuoteNoBlock(t *testing.T) {
	o := &Orchestrator{contextFormatter: ctxflow.NewXMLFormatter()}
	msg := o.buildUserMessage(context.Background(), &RunRequest{
		UserID:             "u1",
		ConversationID:     "c1",
		UserMessageContent: "你好",
		RawUserMessage:     "你好",
	}, nil)

	if msg.XMLContext != nil && strings.Contains(*msg.XMLContext, "document_quote") {
		t.Errorf("无引用时不应出现 document_quote: %s", *msg.XMLContext)
	}
}
