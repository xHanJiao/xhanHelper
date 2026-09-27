import Foundation

/// URL 脱敏（附表 A 硬规则：URL 里的 token 等敏感值禁止进事件）。
///
/// 只做这一件事——去掉 fragment、把敏感查询参数的值替换成 `REDACTED`；其余原样保留
/// （真正的归一与仓库锚在 P12）。不做解码/重排，尽量不改变 `url_raw` 的原貌。
nonisolated enum ActivityUrls {
    private static let sensitiveKeys: Set<String> = [
        "token", "access_token", "refresh_token", "id_token",
        "api_key", "apikey", "key", "password", "passwd", "pwd",
        "secret", "sig", "signature", "auth", "authorization", "session", "sessionid",
    ]

    static func sanitized(_ raw: String) -> String {
        guard !raw.isEmpty else { return raw }
        var s = raw
        if let hash = s.firstIndex(of: "#") { s = String(s[s.startIndex..<hash]) }
        guard let q = s.firstIndex(of: "?") else { return s }
        let base = String(s[s.startIndex..<q])
        let query = String(s[s.index(after: q)...])
        let cleaned = query.split(separator: "&", omittingEmptySubsequences: false).map { item -> String in
            guard let eq = item.firstIndex(of: "=") else { return String(item) }
            let key = String(item[item.startIndex..<eq])
            return sensitiveKeys.contains(key.lowercased()) ? "\(key)=REDACTED" : String(item)
        }.joined(separator: "&")
        return cleaned.isEmpty ? base : "\(base)?\(cleaned)"
    }
}
