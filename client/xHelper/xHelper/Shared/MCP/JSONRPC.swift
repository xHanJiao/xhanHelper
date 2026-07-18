#if os(macOS)
@preconcurrency import Foundation

// JSON-RPC 2.0 编解码。
// 设计决策（见 docs/design/mac-apps/client-side-mcp.md §1.2）：
// payload（params/result/inputSchema）是任意嵌套 JSON，统一用 JSONSerialization 透传，
// 不引入 AnyCodable。注意不能用 Codable + Data 字段——JSONEncoder 会把 Data 编成 base64。

enum JSONRPC {
    // 构造一行 newline-delimited 请求
    nonisolated static func requestData(id: Int64, method: String, params: [String: Any]? = nil) throws -> Data {
        var obj: [String: Any] = ["jsonrpc": "2.0", "id": id, "method": method]
        if let p = params { obj["params"] = p }
        var data = try JSONSerialization.data(withJSONObject: obj)
        data.append(0x0A)
        return data
    }

    // 构造一行通知（无 id，不需要响应）
    nonisolated static func notificationData(method: String, params: [String: Any]? = nil) throws -> Data {
        var obj: [String: Any] = ["jsonrpc": "2.0", "method": method]
        if let p = params { obj["params"] = p }
        var data = try JSONSerialization.data(withJSONObject: obj)
        data.append(0x0A)
        return data
    }
}

// JSON-RPC 2.0 响应（解析自 stdout 的一行）
struct JSONRPCResponse: @unchecked Sendable {
    let id: Int64?
    nonisolated(unsafe) let result: [String: Any]?
    let errorCode: Int?
    let errorMessage: String?

    nonisolated var isError: Bool { errorCode != nil || errorMessage != nil }

    // 解析一行；非响应消息（服务端通知/请求）返回 nil
    nonisolated static func parse(_ line: Data) -> JSONRPCResponse? {
        guard let obj = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any] else {
            return nil
        }
        let id = (obj["id"] as? NSNumber)?.int64Value
        let result = obj["result"] as? [String: Any]
        let error = obj["error"] as? [String: Any]
        if id == nil && result == nil && error == nil {
            return nil // 通知类消息，忽略
        }
        return JSONRPCResponse(
            id: id,
            result: result,
            errorCode: (error?["code"] as? NSNumber)?.intValue,
            errorMessage: error?["message"] as? String
        )
    }
}

// MCP tools/list 返回的工具定义
struct MCPToolDef: @unchecked Sendable {
    let name: String
    let description: String
    nonisolated(unsafe) let inputSchema: [String: Any]?

    // 从 tools/list 的 result 中解析
    nonisolated static func parseList(from result: [String: Any]?) -> [MCPToolDef] {
        guard let tools = result?["tools"] as? [[String: Any]] else { return [] }
        return tools.compactMap { t in
            guard let name = t["name"] as? String else { return nil }
            return MCPToolDef(
                name: name,
                description: t["description"] as? String ?? "",
                inputSchema: t["inputSchema"] as? [String: Any]
            )
        }
    }
}

// MCP 协议错误
struct MCPError: LocalizedError {
    let message: String
    nonisolated var errorDescription: String? { message }
}

// MCP 模块日志转发（LogBuffer 是 @MainActor，MCP 代码跑在 actor/后台线程）
enum MCPLog {
    nonisolated static func info(_ msg: String) {
        Task { @MainActor in LogBuffer.shared.info(msg) }
    }
    nonisolated static func error(_ msg: String) {
        Task { @MainActor in LogBuffer.shared.error(msg) }
    }
}
#endif
