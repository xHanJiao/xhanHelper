import Foundation

final class DirectoryAuthorizationManager {
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

    func isAuthorized(_ path: String) -> Bool {
        let resolved = resolvePath(path)
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
