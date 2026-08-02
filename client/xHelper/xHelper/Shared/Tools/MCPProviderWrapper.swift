#if os(macOS)
import Foundation

final class MCPProviderWrapper: @unchecked Sendable {
    let namespace: String
    private let manager: MCPManager

    init(namespace: String, manager: MCPManager) {
        self.namespace = namespace
        self.manager = manager
    }
}

extension MCPProviderWrapper: LocalToolProvider {

    func toolSchemas() async -> [LocalToolSchema] {
        var schemas = await manager.toolSchemas(for: namespace)
        schemas.append(LocalToolSchema(
            name: MCPManager.readMoreToolName,
            description: "当上一个工具结果过大被分页（结果中 truncated=true）时，按页继续读取剩余内容。需提供 page_token（首页返回，绑定该次分页）和 cursor（页码）。",
            inputSchema: [
                "type": "object",
                "properties": [
                    "cursor": ["type": "integer", "description": "页码，从 2 开始（第 1 页已随原结果返回）"],
                    "page_token": ["type": "string", "description": "分页令牌，与首页 truncated 结果中的 page_token 一致，防止读到已被覆盖的缓存"],
                ],
                "required": ["cursor", "page_token"],
            ],
            isReadOnly: false // 当前无 MCP 使用，全部按写保守标注；readOnlyHint 支持留待未来
        ))
        return schemas
    }

    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        let flatName = "\(namespace)__\(tool)"
        let argsData = (try? JSONSerialization.data(withJSONObject: arguments)) ?? Data("{}".utf8)
        let budget = context.budgetBytes ?? MCPManager.fallbackResultBudgetBytes
        let deadline = context.deadlineSecs ?? 54
        let data = try await manager.callTool(
            flatName: flatName,
            argsJSON: argsData,
            budgetBytes: budget,
            deadlineSecs: deadline
        )
        return LocalToolResult(serverResult: data, serverError: nil, clientAction: nil)
    }
}

#endif
