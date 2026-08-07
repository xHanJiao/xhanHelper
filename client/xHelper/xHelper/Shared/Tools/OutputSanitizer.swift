import Foundation

enum OutputSanitizer {
    private static let patterns: [(NSRegularExpression, String)] = {
        let defs: [(String, String)] = [
            (#"sk-[a-zA-Z0-9]{20,}"#, "sk-***REDACTED***"),
            (#"AKIA[0-9A-Z]{16}"#, "AKIA***REDACTED***"),
            (#"-----BEGIN[^-]*PRIVATE KEY-----[^-]*-----END[^-]*-----"#, "***PRIVATE KEY REDACTED***"),
            (#"Bearer\s+[a-zA-Z0-9._\-]{20,}"#, "Bearer ***REDACTED***"),
            (#"(?:export\s+)?([A-Z_]*KEY)\s*=\s*\S+"#, "$1=***REDACTED***"),
            (#"(?:export\s+)?([A-Z_]*TOKEN)\s*=\s*\S+"#, "$1=***REDACTED***"),
            (#"(?:export\s+)?([A-Z_]*SECRET)\s*=\s*\S+"#, "$1=***REDACTED***"),
            (#"(?:export\s+)?([A-Z_]*PASSWORD)\s*=\s*\S+"#, "$1=***REDACTED***"),
            (#"ghp_[a-zA-Z0-9]{36}"#, "ghp_***REDACTED***"),
            (#"gho_[a-zA-Z0-9]{36}"#, "gho_***REDACTED***"),
            (#"ghu_[a-zA-Z0-9]{36}"#, "ghu_***REDACTED***"),
            (#"ghs_[a-zA-Z0-9]{36}"#, "ghs_***REDACTED***"),
            (#"ghr_[a-zA-Z0-9]{36}"#, "ghr_***REDACTED***"),
        ]
        return defs.compactMap { pattern, replacement in
            guard let regex = try? NSRegularExpression(pattern: pattern, options: []) else { return nil }
            return (regex, replacement)
        }
    }()

    static func sanitize(_ input: String) -> String {
        var result = input
        for (regex, replacement) in patterns {
            result = regex.stringByReplacingMatches(
                in: result,
                options: [],
                range: NSRange(result.startIndex..., in: result),
                withTemplate: replacement
            )
        }
        return result
    }
}
