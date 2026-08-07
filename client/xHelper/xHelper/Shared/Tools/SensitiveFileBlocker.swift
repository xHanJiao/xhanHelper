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

    static func check(_ path: String) -> String? {
        let resolved = (path as NSString).standardizingPath
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
