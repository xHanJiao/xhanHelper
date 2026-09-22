import Foundation

@MainActor
final class LocalToolRegistry {
    static let shared = LocalToolRegistry()

    /// 工具风险等级表（flatName: namespace__tool）。
    /// 未登记的工具默认 L2（低风险写）。L3（高风险写，执行前需用户确认）仅留给
    /// 基础语义有破坏性的工具（如未来终端的 rm/mv）；agent_browser 全部 ≤L2。
    /// shell 例外：工作区内 L2（免确认），区外 L3（常规确认、信任模式可跳），见下方 `shell__` 分支。
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
        // L3 需用户确认：本地文件写入（覆盖/追加/编辑，破坏性操作）
        "fs__write": 3,
        "fs__edit": 3,
    ]

    func riskLevel(for flatName: String) -> Int {
        riskLevels[flatName] ?? 2
    }

    #if os(macOS)
    func riskLevel(for flatName: String, arguments: [String: Any]) -> Int {
        if flatName.hasPrefix("shell__") {
            // 只读命令：工作区内 L2（免确认）/ 区外 L3（常规确认、信任模式可跳）。
            // 写/执行命令：恒 L4（红色确认、工作区内也 L4）。未命中白名单已由 shellBlockedReason 前置拒绝。
            return ShellToolProvider.shellLevel(arguments: arguments)
        }
        if flatName.hasPrefix("fs__") {
            let paths = FileSystemToolProvider.extractPaths(flatName: flatName, arguments: arguments)
            for path in paths {
                if !DirectoryAuthorizationManager.shared.isReadAuthorized(path) {
                    return 3
                }
            }
            // 已授权目录内也按风险表定级（fs__write 恒 L3）
            return riskLevels[flatName] ?? 2
        }
        return riskLevels[flatName] ?? 2
    }

    func fsBlockedPaths(arguments: [String: Any]) -> String? {
        for path in FileSystemToolProvider.extractPaths(flatName: "fs__", arguments: arguments) {
            if let reason = SensitiveFileBlocker.check(path) {
                return reason
            }
        }
        return nil
    }

    /// shell__run 硬阻断（命令缺失 / workdir 未授权 / 语法不支持 / 未命中白名单）。
    /// 返回 nil 表示可继续（allow 自动执行，l4 走人工确认）。
    func shellBlockedReason(arguments: [String: Any]) -> String? {
        ShellToolProvider.blockedReason(arguments: arguments)
    }
    #endif

    /// 工具 schema 描述缓存（flatName → description），供用户可读展示名使用。
    /// clientToolsPayload 构建时填充，动态 MCP 工具注册后随重建自动更新。
    private var schemaDescriptions: [String: String] = [:]

    /// 浏览器工具的简短展示名（确认条标题用，避免显示冗长工具描述）。
    private static let shortNames: [String: String] = [
        "browser__agent_browser_open": "打开网址",
        "browser__agent_browser_close": "关闭所有标签页",
        "browser__agent_browser_close_tab": "关闭标签页",
        "browser__agent_browser_list_tabs": "列出标签页",
        "browser__agent_browser_switch_tab": "切换标签页",
        "browser__agent_browser_read": "读取页面",
        "browser__agent_browser_snapshot": "读取页面快照",
        "browser__agent_browser_get_text": "读取元素文本",
        "browser__agent_browser_get_value": "读取表单值",
        "browser__agent_browser_get_url": "读取网址",
        "browser__agent_browser_get_title": "读取标题",
        "browser__agent_browser_click": "点击页面元素",
        "browser__agent_browser_click_text": "点击文本",
        "browser__agent_browser_cgclick": "真实点击",
        "browser__agent_browser_drag": "拖拽元素",
        "browser__agent_browser_fill": "填写表单",
        "browser__agent_browser_type": "输入文本",
        "browser__agent_browser_press": "按键",
        "browser__agent_browser_select": "选择选项",
        "browser__agent_browser_check": "勾选",
        "browser__agent_browser_uncheck": "取消勾选",
        "browser__agent_browser_scroll": "滚动页面",
        "browser__agent_browser_wait_for_load": "等待加载",
        "browser__agent_browser_wait_for_selector": "等待元素",
        "browser__agent_browser_go_back": "后退",
        "browser__agent_browser_go_forward": "前进",
        "browser__agent_browser_get_media_sources": "获取媒体资源",
        "browser__agent_browser_download": "下载文件",
        // fs 文件探索工具
        "fs__glob": "搜索文件",
        "fs__grep": "搜索内容",
        "fs__read": "读取文件",
        "fs__write": "写入文件",
        "fs__edit": "编辑文件",
        // shell
        "shell__run": "执行命令",
    ]

    /// 用户可读的工具展示名：优先用简短名，其次 schema 的中文 description（截断），未命中兜底 flatName
    func displayName(for flatName: String) -> String {
        if let short = Self.shortNames[flatName] {
            return short
        }
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
                    "is_read_only": s.isReadOnly, // 函数级必备字段；plan 模式已全量禁用（2026-08-14），保留待未来只读模式复用
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
