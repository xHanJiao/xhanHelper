import Foundation

struct Conversation: Identifiable, Codable {
    let id: String
    let userId: String
    let title: String
    let agentId: String?
    let agentName: String?
    let lastExtractedMsgId: String? = nil
    let lastExtractedAt: String? = nil
    /// 会话模式："work"（默认）| "plan"（只读）| "strict"（无工具）；旧数据可能缺失
    let mode: String?
    /// 无痕会话标记；普通会话服务端不返回该字段，故可选。
    /// 注意用 var：let 带默认值不会被 synthesized Decodable 解码，会恒为 nil
    var ephemeral: Bool? = nil
    /// 是否置顶（服务端 pinned_at 非空）。列表已按置顶优先返回。
    var pinned: Bool? = nil
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
        case ephemeral
        case pinned
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}
