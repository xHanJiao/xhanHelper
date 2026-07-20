import Foundation

@MainActor
final class LocalToolRegistry {
    static let shared = LocalToolRegistry()

    private var providers: [String: any LocalToolProvider] = [:]

    func register(_ provider: any LocalToolProvider) {
        providers[provider.namespace] = provider
    }

    func clientToolsPayload() async -> [[String: Any]] {
        var out: [[String: Any]] = []
        for (namespace, provider) in providers {
            let schemas = await provider.toolSchemas()
            for s in schemas {
                var item: [String: Any] = [
                    "server_name": namespace,
                    "name": s.name,
                    "description": s.description,
                ]
                if let params = s.inputSchema {
                    item["parameters"] = params
                }
                out.append(item)
            }
        }
        return out
    }

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
