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

    /// 将 tool_calls_json 展开为独立的 role="tool" 消息，插入 self 之前，
    /// 与 SSE 流式路径产生相同的消息结构。展开后 self 的 toolCallsJSON 置 nil。
    func expandToolCalls() -> [Message] {
        guard let tcs = parsedToolCalls, !tcs.isEmpty else { return [self] }
        var result: [Message] = []
        for tc in tcs {
            result.append(Message(
                id: UUID().uuidString,
                conversationId: conversationId,
                role: "tool",
                content: tc.buildSummary(),
                toolCallsJSON: nil,
                createdAt: createdAt
            ))
        }
        result.append(Message(
            id: id,
            conversationId: conversationId,
            role: role,
            content: content,
            toolCallsJSON: nil,
            createdAt: createdAt
        ))
        return result
    }
}


/// 历史消息中的工具调用条目（从 tool_calls_json 解析）
struct ToolCallHistoryItem: Codable, Identifiable {
    var id: String { "\(tool).\(function)" }
    let tool: String
    let function: String
    let args: [String: AnyCodable]?
    let result: ToolResultData?

    var icon: String {
        switch function {
        case "create_bill": return "plus.circle"
        case "update_bill": return "pencil.circle"
        case "delete_bill": return "trash.circle"
        case "query_bills": return "magnifyingglass.circle"
        default: return "wrench"
        }
    }

    var displayName: String {
        switch function {
        case "create_bill":       return "记账"
        case "update_bill":       return "修改账单"
        case "cancel_bill":       return "作废账单"
        case "query_bills":        return "查询账单"
        case "get_monthly_stats":  return "月度统计"
        case "get_category_stats": return "分类统计"
        case "get_current_time":  return "获取时间"
        case "evaluate":          return "计算"
        case "generate":          return "生成UUID"
        case "record_note":       return "记录笔记"
        case "search":            return "搜索笔记"
        case "create_agent":      return "创建智能体"
        case "list_agents":       return "列出智能体"
        default: return function
        }
    }

    var argsSummary: [(String, String)] {
        var result: [(String, String)] = []
        if let a = args {
            if let v = a["amount"] { result.append(("金额", "\(v.value)元")) }
            if let v = a["item"] { result.append(("项目", "\(v.value)")) }
            if let v = a["category1"] { result.append(("分类", "\(v.value)")) }
            if let v = a["topic"] { result.append(("主题", "\(v.value)")) }
            if let v = a["content"] {
                let s = "\(v.value)"
                result.append(("内容", s.count > 20 ? String(s.prefix(20)) + "..." : s))
            }
            if let v = a["name"] { result.append(("名称", "\(v.value)")) }
            if let v = a["query"] { result.append(("查询", "\(v.value)")) }
        }
        return result
    }

    func buildSummary() -> String {
        var parts: [String] = []
        parts.append(displayName)
        for (key, val) in argsSummary {
            parts.append("\(key): \(val)")
        }
        if let r = result {
            parts.append(r.success ? "✅ 成功" : "❌ \(r.error ?? "失败")")
        }
        return parts.joined(separator: " | ")
    }
}
