import Foundation

// MARK: - LocalToolSchema

struct LocalToolSchema: @unchecked Sendable {
    let name: String
    let description: String
    let inputSchema: [String: Any]?
    /// 只读标记（必备字段，构造时必须显式标注）。
    /// 注（2026-08-14）：服务端 plan 模式已收紧为全量禁用客户端工具，不再依据此字段放行；
    /// 字段保留仅为未来"只读浏览模式"复用。无默认值 = 编译期强制每个函数声明。
    let isReadOnly: Bool
}

// MARK: - ExecuteContext

struct ExecuteContext: Sendable {
    let budgetBytes: Int?
    let deadlineSecs: TimeInterval?
    /// 标签归属（client_tool_call payload 下发）："main"=主循环，"subagent:<convID>"=子任务。
    /// 浏览器工具据此做标签隔离：open 记录归属、list_tabs 标注、close_tab 保护其他归属的标签。
    let owner: String?
}

// MARK: - ClientAction

nonisolated struct ClientAction: Sendable {
    let type: String
    let payload: Data
}

// MARK: - LocalToolResult

nonisolated struct LocalToolResult: Sendable {
    let serverResult: Data?
    let serverError: String?
    let clientAction: ClientAction?
}

// MARK: - LocalToolProvider

protocol LocalToolProvider: AnyObject {
    var namespace: String { get }
    func toolSchemas() async -> [LocalToolSchema]
    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult
}


