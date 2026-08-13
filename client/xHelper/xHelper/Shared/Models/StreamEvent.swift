import Foundation

enum StreamEventType: String {
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
    case done
    case error
}

struct StreamEvent {
    let type: StreamEventType
    let data: String
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
