import Foundation

/// `nonisolated`：状态由并发队列 + UserDefaults 保护，无 UI 依赖；允许从后台任务（shell 解析）调用。
nonisolated final class DirectoryAuthorizationManager: @unchecked Sendable {
    static let shared = DirectoryAuthorizationManager()

    private let permanentKey = "fs_permanent_authorized_dirs"
    private let defaultExpirySecs: TimeInterval = 3600
    private let queue = DispatchQueue(label: "com.xhan.fsauth", attributes: .concurrent)

    private var _temporaryDirs: [String: Date] = [:]

    private var temporaryDirs: [String: Date] {
        get { queue.sync { _temporaryDirs } }
        set { queue.async(flags: .barrier) { self._temporaryDirs = newValue } }
    }

    var permanentDirs: [String] {
        get { UserDefaults.standard.stringArray(forKey: permanentKey) ?? [] }
        set { UserDefaults.standard.set(newValue, forKey: permanentKey) }
    }

    var temporaryExpirySecs: TimeInterval { defaultExpirySecs }

    /// 路径是否落在 Agent 隐式工作区内（App 自有目录，按路径组件匹配）。
    func isWithinAgentWorkspace(_ path: String) -> Bool {
        let resolved = resolvePath(path).lowercased()
        for dir in AgentWorkspace.implicitReadWriteRoots {
            let root = resolvePath(dir).lowercased()
            if resolved == root || resolved.hasPrefix(root + "/") {
                return true
            }
        }
        return false
    }

    /// 供 LLM 的可访问目录摘要（工作区 + 已授权目录 + 只读溢出目录）。
    func accessSummary() -> String {
        var lines: [String] = ["- Agent 工作区（可读写）：\(AgentWorkspace.rootPath)"]
        if permanentDirs.isEmpty {
            lines.append("- 已授权目录（可读写）：无（用户尚未添加；需要其它目录时用户会在「设置 → 文件访问授权」添加）")
        } else {
            lines.append("- 已授权目录（可读写）：\(permanentDirs.joined(separator: "、"))")
        }
        #if os(macOS)
        lines.append("- 工具输出溢出目录（只读，仅 fs 可读）：\(ToolOutputStore.shared.rootPath)")
        #endif
        return lines.joined(separator: "\n")
    }

    /// 工具 description 的目录说明块：可访问目录 + 硬性禁访高危路径。随 client_tools 每条消息下发。
    static func accessDescriptionBlock() -> String {
        let blocked = SensitiveFileBlocker.hardBlockedDisplayPaths.joined(separator: "、")
        return """
        可访问目录（工具参数用绝对路径）：
        \(shared.accessSummary())
        以下高危路径为硬性拦截（读写皆拒、不可豁免），不要尝试：
        \(blocked)
        """
    }

    func isAuthorized(_ path: String) -> Bool {
        let resolved = resolvePath(path)
        // 隐式读写根（Agent 默认工作区）：App 自有，无需手动授权。
        if isWithinAgentWorkspace(resolved) {
            return true
        }
        for dir in permanentDirs {
            if resolved.lowercased().hasPrefix(resolvePath(dir).lowercased()) {
                return true
            }
        }
        cleanExpired()
        for (dir, expires) in temporaryDirs {
            if Date() >= expires { continue }
            if resolved.lowercased().hasPrefix(resolvePath(dir).lowercased()) {
                return true
            }
        }
        return false
    }

    /// 只读授权：常规授权目录之外，额外隐式放行 App 自有目录（如工具结果溢出目录
    /// `tool_output/`），仅供 fs__read/fs__grep/fs__glob 读取，**不放行写**。
    /// 按路径组件匹配（避免 `/a/proj` 误放行 `/a/project`）。
    func isReadAuthorized(_ path: String) -> Bool {
        if isAuthorized(path) { return true }
        #if os(macOS)
        let resolved = resolvePath(path).lowercased()
        for dir in ToolOutputStore.implicitReadOnlyRoots {
            let root = resolvePath(dir).lowercased()
            if resolved == root || resolved.hasPrefix(root + "/") {
                return true
            }
        }
        #endif
        return false
    }

    func authorize(_ path: String, permanent: Bool) {
        let resolved = resolvePath(path)
        if permanent {
            var dirs = permanentDirs
            if !dirs.contains(where: { resolvePath($0).lowercased() == resolved.lowercased() }) {
                dirs.append(resolved)
                permanentDirs = dirs
            }
        } else {
            var current = temporaryDirs
            current[resolved] = Date().addingTimeInterval(temporaryExpirySecs)
            temporaryDirs = current
        }
    }

    func removePermanent(_ path: String) {
        let resolved = resolvePath(path)
        var dirs = permanentDirs
        dirs.removeAll { resolvePath($0).lowercased() == resolved.lowercased() }
        permanentDirs = dirs
    }

    private func cleanExpired() {
        let now = Date()
        var current = temporaryDirs
        let before = current.count
        current = current.filter { $0.value > now }
        if current.count != before {
            temporaryDirs = current
        }
    }

    private func resolvePath(_ path: String) -> String {
        (path as NSString).standardizingPath
    }
}
