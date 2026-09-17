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
    }
}

struct DocumentAnnotationListResponse: Codable {
    let annotations: [DocumentAnnotation]
    let total: Int
}

/// `PATCH /api/annotations/:id` 的响应（服务端只回 id + note，不是整条）
struct AnnotationNoteUpdate: Codable {
    let id: String
    let note: String
}
