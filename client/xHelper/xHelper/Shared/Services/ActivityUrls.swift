import Foundation

/// URL 脱敏（附表 A 硬规则：URL 里的 token 等敏感值禁止进事件）。
///
/// 只做这一件事——去掉 fragment、把敏感值替换成 `REDACTED`；其余原样保留（真正的归一与仓库锚在 P12）。
/// 两道：① 参数名命中敏感名单；② 参数值**百分号解码后**若含 `敏感名=`（凭证藏在 redirect/next 之类的值里）。
/// 不做解码以外的重排，尽量不改变 `url_raw` 的原貌。已知会漏：多层嵌套（值里再编码一层）/ 非 query 编码，
/// 归 P12 归一处理。
nonisolated enum ActivityUrls {
    private static let sensitiveKeys: [String] = [
        "token", "access_token", "refresh_token", "id_token",
        "api_key", "apikey", "api-key", "x-api-key",
        "key", "password", "passwd", "pwd", "secret", "client_secret",
        "sig", "signature", "auth", "authorization", "bearer", "jwt",
        "code", "credential", "session", "sessionid",
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
            let value = String(item[item.index(after: eq)...])
            if isSensitive(key) || (value.removingPercentEncoding.map(containsSensitive) ?? false) {
                return "\(key)=REDACTED"
            }
            return "\(key)=\(value)"
        }.joined(separator: "&")
        return cleaned.isEmpty ? base : "\(base)?\(cleaned)"
    }

    private static func isSensitive(_ key: String) -> Bool {
        sensitiveKeys.contains(key.lowercased())
    }

    /// 解码后的值里是否藏着一个 `敏感名=`（`?token=` / `&code=` / 裸 `token=` 都算）。
    private static func containsSensitive(_ decoded: String) -> Bool {
        let lower = decoded.lowercased()
        return sensitiveKeys.contains { lower.contains("\($0)=") }
    }
}
