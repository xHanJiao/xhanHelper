import Foundation

/// 文档划线引用（「问 AI」）：会话级状态 —— 引用还在就每轮随请求发（`doc_quote`）。
///
/// 服务端把它渲染成 `document_quote` 块注入**本轮 LLM 上下文**（不动用户消息正文，
/// 也不进检索/记忆/摘要）。`quote` 是渲染后 DOM 的纯文本（空白已折叠、无 Markdown 标记），
/// `prefix`/`suffix` 是靠近选区的原始前后文，只用来帮 LLM 认语境。
///
/// 不落 UserDefaults：引用是临时上下文，重启后还挂着反而危险（文档可能已被改写），
/// 而它只有几百字，重新问一次的成本极低。生命周期跟 ChatViewModel（会话/tab）。
struct DocQuote: Codable, Equatable {
    let documentID: String
    let title: String
    let quote: String
    let prefix: String
    let suffix: String

    enum CodingKeys: String, CodingKey {
        case documentID = "document_id"
        case title, quote, prefix, suffix
    }
}
