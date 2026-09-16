import Foundation

/// `nonisolated`：纯静态判定 + UserDefaults/不可变规则，允许从后台任务（shell 解析）调用。
nonisolated enum SensitiveFileBlocker {
    private static let blockedPatterns: [(pattern: String, isRegex: Bool)] = [
        (".env", false),
        (".env.", false),
        (".git-credentials", false),
        ("credentials", false),
        ("secret", false),
        ("id_rsa", false),
        ("id_ed25519", false),
        ("id_ecdsa", false),
        (".pem", false),
        ("/.ssh/", false),
    ]

    private static let blockedExtensions: Set<String> = [
        ".key",
    ]

    // MARK: - 硬性拦截层（不可豁免：任何豁免 / 授权 / 信任模式都不放行）

    /// 高危目录：按路径组件前缀匹配（$HOME 锚定 + 系统）。
    private static let hardBlockedDirs: [String] = {
        let home = NSHomeDirectory()
        return [
            "\(home)/.ssh",
            "\(home)/.gnupg",
            "\(home)/.aws",
            "\(home)/.kube",
            "\(home)/.docker",
            "\(home)/Library/Keychains",
            "/etc/sudoers.d",
        ]
    }()

    /// 高危文件：精确匹配（$HOME 锚定 + 系统）。写进去 ≈ 任意代码执行 / 凭证泄露。
    private static let hardBlockedFiles: [String] = {
        let home = NSHomeDirectory()
        return [
            "\(home)/.zshrc", "\(home)/.zshenv", "\(home)/.zprofile",
            "\(home)/.zlogin", "\(home)/.zlogout", "\(home)/.zsh_history",
            "\(home)/.bashrc", "\(home)/.bash_profile", "\(home)/.bash_login",
            "\(home)/.bash_history", "\(home)/.profile", "\(home)/.inputrc",
            "\(home)/.netrc", "\(home)/.npmrc", "\(home)/.pypirc",
            "\(home)/.git-credentials", "\(home)/.wgetrc", "\(home)/.curlrc",
            "\(home)/.config/fish/config.fish",
            "\(home)/.config/gh/hosts.yml",
            "/etc/passwd", "/etc/shadow", "/etc/master.passwd", "/etc/sudoers",
        ]
    }()

    private static func normalized(_ path: String) -> String {
        trimTrailingSlash((path as NSString).standardizingPath).lowercased()
    }

    /// 路径组件前缀匹配（`/` 根目录特判，避免拼出 `//`）。
    private static func isWithin(_ path: String, _ root: String) -> Bool {
        path == root || path.hasPrefix(root == "/" ? "/" : root + "/")
    }

    /// 是否命中硬性拦截（目录按组件前缀、文件精确）。不受豁免影响。
    static func isHardBlocked(_ path: String) -> Bool {
        let resolved = normalized(path)
        for dir in hardBlockedDirs where isWithin(resolved, normalized(dir)) {
            return true
        }
        return hardBlockedFiles.contains { resolved == normalized($0) }
    }

    /// 路径是否为任一硬性拦截路径的祖先目录（含自身）——递归命令的根目录判定用。
    static func isAncestorOfHardPath(_ path: String) -> Bool {
        let resolved = normalized(path)
        for hp in hardBlockedDirs + hardBlockedFiles where isWithin(normalized(hp), resolved) {
            return true
        }
        return false
    }

    // MARK: - 豁免列表（独立于授权目录：只解除敏感锁，不动授权锁）

    static let exemptionsKey = "fs_sensitive_exempt_paths"

    static var exemptPaths: [String] {
        get { UserDefaults.standard.stringArray(forKey: exemptionsKey) ?? [] }
        set { UserDefaults.standard.set(newValue, forKey: exemptionsKey) }
    }

    static func addExemption(_ path: String) {
        let resolved = (path as NSString).standardizingPath
        // 硬性拦截路径不可豁免
        guard !isHardBlocked(resolved) else { return }
        var paths = exemptPaths
        if !paths.contains(where: { ($0 as NSString).standardizingPath.lowercased() == resolved.lowercased() }) {
            paths.append(resolved)
            exemptPaths = paths
        }
    }

    static func removeExemption(_ path: String) {
        let resolved = (path as NSString).standardizingPath
        var paths = exemptPaths
        paths.removeAll { ($0 as NSString).standardizingPath.lowercased() == resolved.lowercased() }
        exemptPaths = paths
    }

    /// 路径组件级前缀匹配（大小写不敏感，APFS 默认大小写不敏感，与授权管理一致）。
    /// 注意必须按组件匹配：豁免 "…/.git" 不应豁免 "…/.git-credentials"（兄弟文件）。
    static func isExempted(_ path: String) -> Bool {
        let resolved = trimTrailingSlash((path as NSString).standardizingPath).lowercased()
        for p in exemptPaths {
            let candidate = trimTrailingSlash((p as NSString).standardizingPath).lowercased()
            if resolved == candidate || resolved.hasPrefix(candidate + "/") {
                return true
            }
        }
        return false
    }

    private static func trimTrailingSlash(_ path: String) -> String {
        var p = path
        while p.hasSuffix("/"), p.count > 1 {
            p.removeLast()
        }
        return p
    }

    static func check(_ path: String) -> String? {
        let resolved = (path as NSString).standardizingPath

        // 硬性拦截层最先判定：高危路径，豁免 / 授权 / 信任模式一律无效
        if isHardBlocked(resolved) {
            return "BLOCKED: 高危路径，硬性拦截（不可豁免）：\(resolved)"
        }

        // 豁免优先：用户显式放行的路径跳过后续敏感规则（仍受授权目录锁约束）
        if isExempted(resolved) {
            return nil
        }

        // .git 内部（与 glob 的 --glob=!**/.git/** 对齐，避免幽灵文件）
        if resolved.contains("/.git/") || resolved.hasSuffix("/.git") {
            return "BLOCKED: cannot access git internals (.git): \(resolved)"
        }

        let fileName = (resolved as NSString).lastPathComponent.lowercased()
        let ext = (resolved as NSString).pathExtension.lowercased()

        if !ext.isEmpty, ext != "pub", blockedExtensions.contains(".\(ext)") {
            return "BLOCKED: cannot access sensitive file type (.\(ext)): \(resolved)"
        }

        for (pattern, _) in blockedPatterns {
            if fileName.contains(pattern) {
                return "BLOCKED: cannot access sensitive file: \(resolved)"
            }
        }

        return nil
    }
}
