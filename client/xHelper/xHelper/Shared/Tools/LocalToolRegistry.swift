import Foundation

@MainActor
final class LocalToolRegistry {
    static let shared = LocalToolRegistry()

    // MARK: - 风险等级

    /// 工具风险等级（flatName: namespace__tool）——**表与规则在 `ToolRiskLevels` / `ToolConfirmPolicy`**
    /// （纯数据 + 纯映射，镜像进 SwiftPM 单测；改等级前先让 `ToolConfirmPolicyTests` 给出新预期）。
    /// 本类只负责把「运行时才知道的信息」（shell 命令内容、fs 目标路径是否已授权）折算成等级。

    #if os(macOS)
    /// shell / shell_peek 动态定级（只读 L2 免确认、写 L4）；fs 的写/读未授权路径归 3。
    func riskLevel(for flatName: String, arguments: [String: Any]) -> Int {
        if let scope = ShellToolProvider.scope(forFlatName: flatName) {
            // 只读（full 的 allow / peek 全部）→ L2（免确认）。写/执行 → L4。
            // 未命中白名单已由 shellBlockedReason 前置拒绝。
            return ShellToolProvider.shellLevel(arguments: arguments, scope: scope)
        }
        if flatName.hasPrefix("fs__") {
            let paths = FileSystemToolProvider.extractPaths(flatName: flatName, arguments: arguments)
            for path in paths {
                if !DirectoryAuthorizationManager.shared.isReadAuthorized(path) {
                    return 3
                }
            }
            // 已授权目录内也按风险表定级（fs__write/edit 恒 3 → 必确认）
            return ToolRiskLevels.level(for: flatName)
        }
        return ToolRiskLevels.level(for: flatName)
    }

    func fsBlockedPaths(arguments: [String: Any]) -> String? {
        for path in FileSystemToolProvider.extractPaths(flatName: "fs__", arguments: arguments) {
            if let reason = SensitiveFileBlocker.check(path) {
                return reason
            }
        }
        return nil
    }

    /// shell / shell_peek 的硬阻断（命令缺失 / workdir 未授权 / 语法不支持 / 未命中白名单）。
    /// 返回 nil 表示可继续（allow 自动执行，l4 走人工确认）。
    func shellBlockedReason(flatName: String, arguments: [String: Any]) -> String? {
        guard let scope = ShellToolProvider.scope(forFlatName: flatName) else { return nil }
        return ShellToolProvider.blockedReason(arguments: arguments, scope: scope)
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
        "shell_peek__run": "查看命令",
        // calendar
        "calendar__list_events": "查看日程",
        "calendar__search_events": "搜索日程",
        "calendar__create_event": "创建日程",
        "calendar__update_event": "修改日程",
        "calendar__delete_event": "删除日程",
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
                    "is_read_only": s.isReadOnly, // 函数级必备字段；plan 模式只放行 fs 的 glob/grep/read（服务端自有白名单），该字段不参与放行
                ]
                if let groupDesc = provider.groupDescription {
                    item["server_description"] = groupDesc // 组级描述，服务端在目录/定义里透传
                }
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
