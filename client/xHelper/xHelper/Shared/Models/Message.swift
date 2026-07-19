import Foundation

struct Message: Identifiable, Codable {
    let id: String
    let conversationId: String
    let role: String         // user | assistant | tool
    let content: String
    let toolCallsJSON: String?
    let reasoningContent: String? = nil
    let memoryIds: [String]? = nil
    let toolCallId: String? = nil
    let createdAt: String

    var chartData: ChartData? = nil

    enum CodingKeys: String, CodingKey {
        case id
        case conversationId = "conversation_id"
        case role
        case content
        case toolCallsJSON = "tool_calls_json"
        case reasoningContent = "reasoning_content"
        case memoryIds = "memory_ids"
        case toolCallId = "tool_call_id"
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

    /// 返回清除 tool_calls_json 后的消息，不展开为独立 tool 消息
    func withoutToolCalls() -> [Message] {
        return [Message(
            id: id,
            conversationId: conversationId,
            role: role,
            content: content,
            toolCallsJSON: nil,
            createdAt: createdAt
        )]
    }
}
