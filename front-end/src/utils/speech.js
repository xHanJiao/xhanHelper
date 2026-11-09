// 句级切分：与 macOS VoiceChatService.cutSentences 同一套规则（见 design.md C2）。
// 句末标点处切；第一句额外在 12 字后的逗号处切开，让首包音频尽早响。

const SENTENCE_END = /[。！？!?；;…\n]/
const CLAUSE_END = /[，,、]/

/**
 * 从 `consumed` 开始切句，返回切出的句子与新的消费位置。
 * 未以句末标点结尾的尾巴不返回，由调用方继续累积。
 */
export function cutSentences(text, consumed, first) {
  const out = []
  let start = consumed
  let allowClause = first
  for (let i = consumed; i < text.length; i++) {
    const ch = text[i]
    const length = i - start + 1
    if (SENTENCE_END.test(ch) || (allowClause && length >= 12 && CLAUSE_END.test(ch))) {
      out.push(text.slice(start, i + 1))
      start = i + 1
      allowClause = false
    }
  }
  return { sentences: out, consumed: start }
}
