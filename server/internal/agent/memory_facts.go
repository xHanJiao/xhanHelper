package agent

import (
	"strings"
	"unicode"
)

// verifyVerbatimFacts 逐字校验：每条 fact 必须是基准片段里**某一条**（去空白后）的一段连续子串。
//
// 基准是一组「原文片段」，有两个调用方、两种含义：
//   - 抽取段 / inline 入口：基准 = 用户消息原文（助手说的话不算来源）
//   - 决策循环写入侧（filterWrittenFacts）：基准 = 已校验过的候选原话（循环的第二手 write 也不能改写）
//
// 判据本身与「片段从哪来」无关，所以两处共用一个函数。
//
// 为什么按单条比而不是把基准拼成一大段再比：拼接会放过「两条各取半句接起来」的伪造。
// 为什么先去空白：模型照抄长文本时常改换行/空格，空白差异不构成「改写」；改字、改标点、换词仍然命中不了。
//
// 返回保留的 facts（保序）与被丢弃的条数。调用方决定怎么记日志——本函数不打日志，保持纯函数可测。
func verifyVerbatimFacts(facts, sources []string) (kept []string, dropped int) {
	if len(facts) == 0 {
		return nil, 0
	}
	norm := make([]string, 0, len(sources))
	for _, c := range sources {
		if s := stripSpaces(c); s != "" {
			norm = append(norm, s)
		}
	}
	kept = make([]string, 0, len(facts))
	for _, f := range facts {
		quote := stripSpaces(f)
		if quote != "" && matchesAnySource(norm, quote) {
			kept = append(kept, strings.TrimSpace(f))
			continue
		}
		dropped++
	}
	return kept, dropped
}

// stripSpaces 去掉全部空白字符（含换行/制表/全角空格）。
func stripSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func matchesAnySource(sources []string, quote string) bool {
	for _, s := range sources {
		if strings.Contains(s, quote) {
			return true
		}
	}
	return false
}

// inlineFactVerifyMsgLimit — inline 校验回查的 user 消息条数。
// inline 是「当场记」，用户的原话就在最近这几轮里，不用读整个会话（content 是 longtext）。
const inlineFactVerifyMsgLimit = 50
