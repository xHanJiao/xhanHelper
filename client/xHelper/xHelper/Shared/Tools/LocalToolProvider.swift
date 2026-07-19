import Foundation

// MARK: - LocalToolSchema

struct LocalToolSchema: @unchecked Sendable {
    let name: String
    let description: String
    let inputSchema: [String: Any]?
}

// MARK: - ExecuteContext

struct ExecuteContext: Sendable {
    let budgetBytes: Int?
    let deadlineSecs: TimeInterval?
}

// MARK: - ClientAction

struct ClientAction: Sendable {
    let type: String
    let payload: Data
}

// MARK: - LocalToolResult

struct LocalToolResult: Sendable {
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


