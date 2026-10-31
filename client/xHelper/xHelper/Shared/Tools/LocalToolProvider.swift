import Foundation

// MARK: - LocalToolSchema

struct LocalToolSchema: @unchecked Sendable {
    let name: String
    let description: String
    let inputSchema: [String: Any]?
    /// 只读标记（必备字段，构造时必须显式标注）。
    /// 注（2026-10-31）：服务端 plan 模式只按自己的白名单放行 fs 的 glob/grep/read，
    /// 不读该字段；字段保留仅供未来"只读浏览模式"复用。无默认值 = 编译期强制每个函数声明。
    let isReadOnly: Bool
}

// MARK: - ExecuteContext

struct ExecuteContext: Sendable {
    let budgetBytes: Int?
    let deadlineSecs: TimeInterval?
    /// 标签归属（client_tool_call payload 下发）："main:<convID>"=主循环，"subagent:<convID>"=子任务。
    /// 浏览器工具据此做标签隔离：open 记录归属、list_tabs 标注、close_tab 保护其他归属的标签。
    let owner: String?
    /// 会话上下文（client_tool_call payload 下发）：浏览器事件挂会话/轮次/父 bridge 事件用。
    let conversationId: String?
    let turnId: String?
    let bridgeEventId: String?

    init(budgetBytes: Int?, deadlineSecs: TimeInterval?, owner: String?,
         conversationId: String? = nil, turnId: String? = nil, bridgeEventId: String? = nil) {
        self.budgetBytes = budgetBytes
        self.deadlineSecs = deadlineSecs
        self.owner = owner
        self.conversationId = conversationId
        self.turnId = turnId
        self.bridgeEventId = bridgeEventId
    }
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

extension LocalToolProvider {
    /// 组级描述（可选）：随 client_tools 上报为 `server_description`，服务端在工具目录/定义里透传。
    /// 同一 namespace 的所有函数共用；缺省由服务端回退「Mac 客户端本地工具（组名）」。
    /// 必须字节稳定——不要拼动态内容（动态块放函数 description）。
    var groupDescription: String? { nil }
}


