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

    // 从 toolCallsJSON 解析工具调用列表（历史消息用）
    var parsedToolCalls: [ToolCallHistoryItem]? {
        guard let json = toolCallsJSON,
              let data = json.data(using: .utf8),
              let items = try? JSONDecoder().decode([ToolCallHistoryItem].self, from: data)
        else { return nil }
        return items.isEmpty ? nil : items
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


/// 历史消息中的工具调用条目（从 tool_calls_json 解析）
struct ToolCallHistoryItem: Codable, Identifiable {
    var id: String { "\(tool).\(function)" }
    let tool: String
    let function: String
    let args: [String: AnyCodable]?

    var icon: String {
        switch function {
        case "create_bill": return "plus.circle"
        case "update_bill": return "pencil.circle"
        case "delete_bill": return "trash.circle"
        case "query_bills": return "magnifyingglass.circle"
        default: return "wrench"
        }
    }
}
