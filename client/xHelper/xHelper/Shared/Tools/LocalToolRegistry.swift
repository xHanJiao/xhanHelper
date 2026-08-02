import Foundation

@MainActor
final class LocalToolRegistry {
    static let shared = LocalToolRegistry()

    /// 工具风险等级表（flatName: namespace__tool）。
    /// 未登记的工具默认 L2（低风险写）。L3（高风险写，执行前需用户确认）仅留给
    /// 基础语义有破坏性的工具（如未来终端的 rm/mv）；agent_browser 全部 ≤L2。
    /// 等级仅供客户端执行控制，不随 schema 下发给 LLM。
    private let riskLevels: [String: Int] = [
        // L1 只读
        "browser__agent_browser_snapshot": 1,
        "browser__agent_browser_read": 1,
        "browser__agent_browser_get_text": 1,
        "browser__agent_browser_get_value": 1,
        "browser__agent_browser_get_url": 1,
        "browser__agent_browser_get_title": 1,
        "browser__agent_browser_list_tabs": 1,
        "browser__agent_browser_wait_for_load": 1,
        "browser__agent_browser_wait_for_selector": 1,
        // L3 需用户确认：打开任意 URL（可能导航到任意站点）
        "browser__agent_browser_open": 3,
        // L3 需用户确认：下载文件到本地（写入磁盘，需用户同意）
        "browser__agent_browser_download": 3,
        // L3 需用户确认：真实系统级点击/拖拽（注入系统事件，可能打断用户操作/点错目标）
        "browser__agent_browser_cgclick": 3,
        "browser__agent_browser_drag": 3,
    ]

    func riskLevel(for flatName: String) -> Int {
        riskLevels[flatName] ?? 2
    }

    /// 工具 schema 描述缓存（flatName → description），供用户可读展示名使用。
    /// clientToolsPayload 构建时填充，动态 MCP 工具注册后随重建自动更新。
    private var schemaDescriptions: [String: String] = [:]

    /// 用户可读的工具展示名：优先用 schema 的中文 description（截断），未命中兜底 flatName
    func displayName(for flatName: String) -> String {
        guard let desc = schemaDescriptions[flatName] else { return flatName }
        let trimmed = desc.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.count > 20 ? String(trimmed.prefix(20)) + "…" : trimmed
    }

    private var providers: [String: any LocalToolProvider] = [:]

    func register(_ provider: any LocalToolProvider) {
        providers[provider.namespace] = provider
    }

    func clientToolsPayload() async -> [[String: Any]] {
        var out: [[String: Any]] = []
        for (namespace, provider) in providers {
            let schemas = await provider.toolSchemas()
            for s in schemas {
                let flat = "\(namespace)__\(s.name)"
                schemaDescriptions[flat] = s.description
                var item: [String: Any] = [
                    "server_name": namespace,
                    "name": s.name,
                    "description": s.description,
                    "is_read_only": s.isReadOnly, // 函数级必备字段：服务端 plan 模式依据它拦截写工具
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
