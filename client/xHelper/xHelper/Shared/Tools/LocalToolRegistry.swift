import Foundation

actor LocalToolRegistry {
    static let shared = LocalToolRegistry()

    private var providers: [String: any LocalToolProvider] = [:]

    // MARK: 注册

    func register(_ provider: any LocalToolProvider) {
        providers[provider.namespace] = provider
    }

    // MARK: Schema 合并

    func clientToolsPayload() async -> [[String: Any]] {
        var out: [[String: Any]] = []
        for (namespace, provider) in providers {
            let schemas = await provider.toolSchemas()
            for s in schemas {
                out.append(Self.buildPayloadItem(namespace: namespace, schema: s))
            }
        }
        return out
    }

    nonisolated private static func buildPayloadItem(namespace: String, schema: LocalToolSchema) -> [String: Any] {
        var item: [String: Any] = [
            "server_name": namespace,
            "name": schema.name,
            "description": schema.description,
        ]
        if let params = schema.inputSchema {
            item["parameters"] = params
        }
        return item
    }

    // MARK: 执行路由

    func execute(
        flatName: String,
        arguments: [String: Any],
        context: ExecuteContext
    ) async throws -> LocalToolResult {
        guard let sep = flatName.range(of: "__") else {
            throw LocalToolError.invalidName(flatName)
        }
        let namespace = String(flatName[..<sep.lowerBound])
        let tool = String(flatName[sep.upperBound...])

        guard let provider = providers[namespace] else {
            throw LocalToolError.unknownNamespace(namespace)
        }

        return try await provider.execute(tool: tool, arguments: arguments, context: context)
    }
}

enum LocalToolError: Error {
    case invalidName(String)
    case unknownNamespace(String)
}
