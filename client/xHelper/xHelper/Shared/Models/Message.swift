import Foundation

struct Message: Identifiable, Codable {
    let id: String
    let conversationId: String
    let role: String         // user | assistant
    let content: String
    let toolCallsJSON: String?
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case conversationId = "conversation_id"
        case role
        case content
        case toolCallsJSON = "tool_calls_json"
        case createdAt = "created_at"
    }

    // 本地构建 user 消息（未入库前）
    static func localUser(text: String, conversationId: String) -> Message {
        Message(
            id: UUID().uuidString,
            conversationId: conversationId,
            role: "user",
            content: text,
            toolCallsJSON: nil,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
    }

    // 本地构建 streaming assistant 消息
    static func streamingAssistant(conversationId: String) -> Message {
        Message(
            id: "__streaming__",
            conversationId: conversationId,
            role: "assistant",
            content: "",
            toolCallsJSON: nil,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
    }
}
