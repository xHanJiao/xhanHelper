import Foundation

/// 文档划线批注（服务端 `model.DocumentAnnotation` 的镜像）。
///
/// 锚点四元组与偏移由**服务端**在 Markdown 源文上算；客户端要画的是 marked 渲染后的 DOM，
/// 两边字符流不同，所以客户端只拿 `quote` + `prefix`/`suffix` 在 DOM 文本流上再定位一次
/// （docs/design/document-highlight-annotation/design.md §4.4）。`orphaned` 由服务端唯一裁定。
struct DocumentAnnotation: Identifiable, Codable, Equatable {
    let id: String
    let documentId: String
    /// 划中的原文（纯文本，无 Markdown 标记）
    let quote: String
    /// 锚点前后文快照，多重命中时用于消歧
    let prefix: String
    let suffix: String
    /// 批注正文；空 = 纯划线
    var note: String
    /// 源文 rune 偏移（客户端不用它定位，仅排序/调试）
    let startOffset: Int
    let endOffset: Int
    /// 服务端重定位失败 → 已失活，正文里不画
    let orphaned: Bool
    /// 服务端走夹逼重定位时改写过 `quote`（让「原文被改了」这件事可见）
    let quoteRewritten: Bool
    let createdAt: String
    let updatedAt: String
    /// 该批注下的回复总数（列表接口附带）。可选：老服务端不带此字段时不致整条解码失败。
    var replyCount: Int?
    /// 最后一条回复（折叠摘要用）。可选，同上。
    var lastReply: AnnotationReply?

    enum CodingKeys: String, CodingKey {
        case id
        case documentId = "document_id"
        case quote, prefix, suffix, note
        case startOffset = "start_offset"
        case endOffset = "end_offset"
        case orphaned
        case quoteRewritten = "quote_rewritten"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case replyCount = "reply_count"
        case lastReply = "last_reply"
    }

    /// 该批注当前展示的回复条数（服务端计数优先，回退到本地已知条数由调用方补充）
    var totalReplies: Int { replyCount ?? 0 }
}

struct DocumentAnnotationListResponse: Codable {
    let annotations: [DocumentAnnotation]
    let total: Int
}

/// 批注下的一条讨论回复（服务端 `model.DocumentAnnotationReply` 的镜像）。
/// 根批注是 `DocumentAnnotation.note`，这张表只放其下的往复（user 追问 / assistant 回复）。
struct AnnotationReply: Identifiable, Codable, Equatable {
    let id: String
    let annotationId: String
    /// "user" | "assistant"
    let role: String
    let content: String
    /// "button" | "followup" | "chat"（生成来源，仅排查/展示用）
    let source: String
    let createdAt: String

    var isAssistant: Bool { role == "assistant" }

    enum CodingKeys: String, CodingKey {
        case id
        case annotationId = "annotation_id"
        case role, content, source
        case createdAt = "created_at"
    }
}

struct AnnotationReplyListResponse: Codable {
    let replies: [AnnotationReply]
    let total: Int
}

/// `POST /api/annotations/:id/reply` 的响应：本次新增的回复（含可选追问）+ 最新总数。
struct AnnotationReplyCreateResponse: Codable {
    let added: [AnnotationReply]
    let replyCount: Int

    enum CodingKeys: String, CodingKey {
        case added
        case replyCount = "reply_count"
    }
}

/// `PATCH /api/annotations/:id` 的响应（服务端只回 id + note，不是整条）
struct AnnotationNoteUpdate: Codable {
    let id: String
    let note: String
}
