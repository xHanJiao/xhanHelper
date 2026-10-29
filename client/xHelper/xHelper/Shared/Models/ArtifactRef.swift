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

/// `GET /api/artifacts` 的列表项（右栏「灵阅」）：**不含正文**，点开才按 id 拉。
struct ArtifactSummary: Codable, Identifiable {
    let id: String
    let title: String
    let summary: String
    let conversationId: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id, title, summary
        case conversationId = "conversation_id"
        case createdAt = "created_at"
    }
}

struct ArtifactListResponse: Codable {
    let items: [ArtifactSummary]
    let total: Int
}
