#if os(macOS)
import Foundation
import Security

/// 本地后端密钥/可选覆盖项的读写：**密钥走 Keychain**，model / base_url 走 UserDefaults（非机密）。
///
/// 读取时回退到进程环境变量——开发便利：仍习惯在 Xcode scheme 里填 key 的老流程也照常能用
/// （Keychain 有值则以 Keychain 为准）。
enum LocalSecretsStore {
    static let modelDefaultsKey = "xhan_llm_model"
    static let baseURLDefaultsKey = "xhan_llm_base_url"

    static func load() -> LocalSecrets {
        let env = ProcessInfo.processInfo.environment
        let defaults = UserDefaults.standard
        return LocalSecrets(
            llmKey: LocalKeychain.string(.llmKey) ?? env["LLM_API_KEY"] ?? "",
            embeddingKey: LocalKeychain.string(.embeddingKey) ?? env["EMBEDDING_API_KEY"] ?? "",
            llmModel: defaults.string(forKey: modelDefaultsKey) ?? "",
            llmBaseURL: defaults.string(forKey: baseURLDefaultsKey) ?? ""
        )
    }

    static var isConfigured: Bool { load().isComplete }

    /// - Returns: `errSecSuccess`，或第一个失败的状态码。
    ///   **必须返回**：写不进钥匙串 = 配置没生效，UI 得报出来（否则症状是「点继续毫无反应」）。
    @discardableResult
    static func save(_ raw: LocalSecrets) -> OSStatus {
        let s = raw.normalized()
        var failure = OSStatus(errSecSuccess)
        // 空 key 不覆盖已有值：UI 已用 isComplete 拦截，这里是契约兜底——
        // 任何漏拦的调用都不该把已配好的 key 冲成空串（keychain 存空串 = 事后读出来像「没配」）。
        if !s.llmKey.isEmpty {
            let st = LocalKeychain.set(s.llmKey, for: .llmKey)
            if st != errSecSuccess { failure = st }
        }
        if !s.embeddingKey.isEmpty {
            let st = LocalKeychain.set(s.embeddingKey, for: .embeddingKey)
            if st != errSecSuccess { failure = st }
        }
        let defaults = UserDefaults.standard
        defaults.set(s.llmModel, forKey: modelDefaultsKey)
        defaults.set(s.llmBaseURL, forKey: baseURLDefaultsKey)
        return failure
    }

    static func clear() {
        LocalKeychain.delete(.llmKey)
        LocalKeychain.delete(.embeddingKey)
        let defaults = UserDefaults.standard
        defaults.removeObject(forKey: modelDefaultsKey)
        defaults.removeObject(forKey: baseURLDefaultsKey)
    }
}
#endif
