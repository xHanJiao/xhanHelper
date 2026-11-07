import Foundation

enum StreamEventType: String {
    case meta             // 本轮身份（turn_id / conversation_id）：续传锚点的起点
    case plan             // 执行计划
    case planNodeStart    // 节点开始
    case planNodeDone     // 节点完成
    case planDone         // 计划完成
    case thought          // 思考过程
    case thoughtDelta     // 思考内容增量（Pro 流式思维链）
    case thoughtDone      // 思考完成
    case status           // 状态提示
    case verify           // 响应检验 verify:checking|done|retry
    case message     // text_delta
    case toolCall    // tool_call
    case toolResult  // tool_result
    case clientToolCall // client_tool_call — 服务端要求客户端执行本地工具
    case subagent    // 子任务活动面板事件（start/end/status/tool_call/tool_result，JSON 含 conv_id）
    case remindersChanged // reminders_changed — 服务端通知有新提醒需同步
    case idle        // 续传：服务端没有正在跑的本轮
    case reset       // 续传：连续性断了（buffer 被裁 / 轮次不匹配），按历史重载
    case done
    case error
}

struct StreamEvent {
    let type: StreamEventType
    let data: String
    /// SSE `id:` —— 同轮内单调递增的续传锚点（每轮从 1 开始）。
    let id: String?

    init(type: StreamEventType, data: String, id: String? = nil) {
        self.type = type
        self.data = data
        self.id = id
    }
}

extension StreamEventType {
    /// SSE `event:` 名 → 事件类型。服务端 `controller/chat.go` 的转发 switch 是唯一事实来源；
    /// 认不出的名字返回 nil，由调用方记一次 warning（别静默丢，也别每轮刷屏）。
    static func from(eventName: String) -> StreamEventType? {
        switch eventName {
        case "meta":              return .meta
        case "plan":              return .plan
        case "plan_node_start":   return .planNodeStart
        case "plan_node_done":    return .planNodeDone
        case "plan_done":         return .planDone
        case "thought":           return .thought
        case "thought_delta":     return .thoughtDelta
        case "thought_done":      return .thoughtDone
        case "status":            return .status
        case "verify":            return .verify
        case "text_delta", "message": return .message
        case "tool_call":         return .toolCall
        case "tool_result":       return .toolResult
        case "client_tool_call":  return .clientToolCall
        case "subagent":          return .subagent
        case "reminders_changed": return .remindersChanged
        case "idle":              return .idle
        case "reset":             return .reset
        case "done":              return .done
        case "error":             return .error
        default:                  return nil
        }
    }
}


struct PlanNodeJSON: Codable {
    let id: String
    let label: String
    let status: String
}

struct PlanEdgeJSON: Codable {
    let from: String
    let to: String
}
