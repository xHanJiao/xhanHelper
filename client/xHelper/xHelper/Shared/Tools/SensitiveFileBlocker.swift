import Foundation

enum SensitiveFileBlocker {
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

    private static let blockedPaths: Set<String> = {
        let home = NSHomeDirectory()
        return [
            "\(home)/.aws/credentials",
            "\(home)/Library/Keychains",
            "/etc/passwd",
            "/etc/shadow",
            "/etc/master.passwd",
        ]
    }()

    // MARK: - 豁免列表（独立于授权目录：只解除敏感锁，不动授权锁）

    static let exemptionsKey = "fs_sensitive_exempt_paths"

    static var exemptPaths: [String] {
        get { UserDefaults.standard.stringArray(forKey: exemptionsKey) ?? [] }
        set { UserDefaults.standard.set(newValue, forKey: exemptionsKey) }
    }

    static func addExemption(_ path: String) {
        let resolved = (path as NSString).standardizingPath
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

        // 豁免优先：用户显式放行的路径跳过全部敏感规则（仍受授权目录锁约束）
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

        for bp in blockedPaths {
            if resolved.hasPrefix(bp) {
                return "BLOCKED: cannot access sensitive location: \(resolved)"
            }
        }

        for (pattern, _) in blockedPatterns {
            if fileName.contains(pattern) {
                return "BLOCKED: cannot access sensitive file: \(resolved)"
            }
        }

        return nil
    }
}
