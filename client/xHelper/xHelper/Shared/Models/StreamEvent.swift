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
    case message     // text_delta
    case toolCall    // tool_call
    case toolResult  // tool_result
    case done
    case error
}

struct StreamEvent {
    let type: StreamEventType
    let data: String
}

struct ToolCallData: Codable {
    let tool: String
    let function: String
    let args: [String: AnyCodable]?

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
        default: return function
        }
    }

    var icon: String {
        switch function {
        case "create_bill": return "plus.circle"
        case "update_bill": return "pencil.circle"
        case "delete_bill": return "trash.circle"
        case "query_bills": return "magnifyingglass.circle"
        default: return "wrench"
        }
    }

    var argsSummary: [(String, String)] {
        var result: [(String, String)] = []
        if let a = args {
            if let v = a["amount"] { result.append(("金额", "\(v.value)元")) }
            if let v = a["item"] { result.append(("项目", "\(v.value)")) }
            if let v = a["category1"] { result.append(("分类", "\(v.value)")) }
            if let v = a["id"] { result.append(("ID", "\(v.value)")) }
        }
        return result
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

struct ToolResultData: Codable {
    let success: Bool
    let error: String?
}

// 简单 AnyCodable 替代 — 解析 tool_call JSON args
struct AnyCodable: Codable {
    let value: Any

    init(_ value: Any) { self.value = value }

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let s = try? container.decode(String.self) { value = s }
        else if let n = try? container.decode(Double.self) { value = n }
        else if let b = try? container.decode(Bool.self) { value = b }
        else if let i = try? container.decode(Int.self) { value = i }
        else { value = "" }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        if let s = value as? String { try container.encode(s) }
        else if let n = value as? Double { try container.encode(n) }
        else if let i = value as? Int { try container.encode(i) }
        else if let b = value as? Bool { try container.encode(b) }
    }
}
