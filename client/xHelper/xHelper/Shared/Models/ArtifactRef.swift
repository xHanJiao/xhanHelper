import SwiftUI

/// 消息里的一条 HTML 产物引用（服务端 `artifacts` 字段下发）。
/// 只带 id/标题/摘要——**正文永远不在这里**，点开才按 id 拉。
struct ArtifactRef: Codable, Identifiable, Hashable {
    let id: String
    var type: String = "html"
    var title: String = ""
    var summary: String = ""
}

/// `GET /api/artifacts/:id` 的响应（唯一带正文的地方）。
struct ArtifactPayload: Codable, Identifiable {
    let id: String
    let title: String
    let summary: String
    let html: String
}

/// `GET /api/artifacts/:id/html` 的结果：304 时 `html == nil`，调用方用本地缓存。
struct ArtifactHTMLResponse {
    let notModified: Bool
    let html: String?
    let etag: String?
}

/// `GET /api/artifacts` 的列表项（右栏「灵阅」）：**不含正文**，点开才按 id 拉。
struct ArtifactSummary: Codable, Identifiable {
    let id: String
    let title: String
    let summary: String
    /// 逗号分隔的标签（服务端归一化过）。必填规则上线前生成的老页为空。
    var tags: String? = nil
    let conversationId: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id, title, summary, tags
        case conversationId = "conversation_id"
        case createdAt = "created_at"
    }
}

struct ArtifactListResponse: Codable {
    let items: [ArtifactSummary]
    let total: Int
}

/// `GET /api/artifacts/tags` 的响应（复用文档词云的同款模型）。
struct ArtifactTagListResponse: Codable {
    let tags: [CloudTag]
    let edges: [TagEdge]
}

/// `POST /api/artifacts/:id/share` 的响应：token + 相对路径（客户端用 baseURL 拼绝对地址）。
struct ArtifactShare: Codable {
    let token: String
    let url: String
}

/// `DELETE /api/artifacts/:id/share` 的响应：`revoked=false` 表示本来就没分享。
struct ArtifactUnshare: Codable {
    let revoked: Bool
}
