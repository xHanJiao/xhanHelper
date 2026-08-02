import Foundation

struct Conversation: Identifiable, Codable {
    let id: String
    let userId: String
    let title: String
    let agentId: String?
    let agentName: String?
    let lastExtractedMsgId: String? = nil
    let lastExtractedAt: String? = nil
    /// 会话模式："work"（默认）| "plan"（只读讨论状态）；旧数据可能缺失
    let mode: String?
    let createdAt: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case title
        case agentId = "agent_id"
        case agentName = "agent_name"
        case lastExtractedMsgId = "last_extracted_msg_id"
        case lastExtractedAt = "last_extracted_at"
        case mode
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}
