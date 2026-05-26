package pipeline

import (
	"context"
	"log"
	"strings"
	"unicode/utf8"
)

// 意图关键词 — 短消息包含这些词时不跳过记忆检索
var intentKeywords = []string{"帮我", "查一下", "记一下", "算", "搜", "找"}

// ShortInputCheckStep 判定短输入是否应跳过记忆检索
type ShortInputCheckStep struct{}

func NewShortInputCheckStep() *ShortInputCheckStep { return &ShortInputCheckStep{} }

func (s *ShortInputCheckStep) Name() string                       { return "shortInputCheck" }
func (s *ShortInputCheckStep) Requires() []string                 { return []string{"raw_msg"} }
func (s *ShortInputCheckStep) Produces() []string                 { return []string{"skip_retrieval", "is_short"} }
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

	skipRetrieval := false
	if isShort && !isQuestion && !hasIntent {
		skipRetrieval = true
		log.Printf("[pipeline] shortInputCheck: skip_retrieval=true (chars=%d, bytes=%d, question=%v, intent=%v)", charLen, byteLen, isQuestion, hasIntent)
	} else {
		log.Printf("[pipeline] shortInputCheck: skip_retrieval=false (chars=%d, bytes=%d, question=%v, intent=%v)", charLen, byteLen, isQuestion, hasIntent)
	}

	pc.SkipRetrieval = skipRetrieval
	pc.IsShort = isShort
	return ctx, nil
}
