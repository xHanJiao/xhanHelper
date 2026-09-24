#if os(macOS)
import Foundation
import Security

/// 本地后端密钥/可选覆盖项的读写：**密钥走 Keychain**，model / base_url 走 UserDefaults（非机密）。
///
/// **刻意不读进程环境变量**：`isConfigured` 只认 Keychain。否则 scheme / shell 里的
/// `LLM_API_KEY` 会把 key 顶上来，而界面显示「未配置」——症状是「清了密钥也回不去首次配置」。
/// 密钥一律由用户在设置页手填（构建期注入密钥是坏味道：key 会落进 scheme 文件）。
enum LocalSecretsStore {
    static let modelDefaultsKey = "xhan_llm_model"
    static let baseURLDefaultsKey = "xhan_llm_base_url"

    /// 密钥与覆盖项都按环境分家：dev / prod 是两个独立沙盒，在 dev 里换供应商、换 key、换模型
    /// 不该动到 prod。（旧版是两环境共享的，下面 `loadKey` 会把旧项一次性迁进本环境。）
    private static var envSuffix: String { LocalPaths.isDev ? "_dev" : "_prod" }

    static func load() -> LocalSecrets {
        let defaults = UserDefaults.standard
        return LocalSecrets(
            llmKey: loadKey(LocalKeychain.llmKeyAccount, legacy: .legacyLLMKey),
            embeddingKey: loadKey(LocalKeychain.embeddingKeyAccount, legacy: .legacyEmbeddingKey),
            llmModel: defaults.string(forKey: modelDefaultsKey + envSuffix) ?? "",
            llmBaseURL: defaults.string(forKey: baseURLDefaultsKey + envSuffix) ?? ""
        )
    }

    /// 读本环境的密钥项；本环境还没有、旧版共享项有 → 复制一份（一次性迁移，省得重填）。
    ///
    /// 迁过就打标记：否则在设置页「清除密钥」后，下次 load 又会从旧项复制回来（等于清不掉）。
    /// 旧项本身不删——另一个环境可能还没迁过。
    private static func loadKey(_ account: LocalKeychain.Account, legacy: LocalKeychain.Account) -> String {
        if let v = LocalKeychain.string(account), !v.isEmpty { return v }
        let defaults = UserDefaults.standard
        let flag = "xhan_key_migrated_" + account.rawValue
        guard !defaults.bool(forKey: flag) else { return "" }
        defaults.set(true, forKey: flag)
        guard let old = LocalKeychain.string(legacy), !old.isEmpty else { return "" }
        let status = LocalKeychain.set(old, for: account)
        if status != errSecSuccess {
            Task { @MainActor in
                LogBuffer.shared.warning("密钥迁移到本环境失败（\(account.rawValue)）：\(LocalKeychain.describe(status))")
            }
        }
        return old
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
            let st = LocalKeychain.set(s.llmKey, for: LocalKeychain.llmKeyAccount)
            if st != errSecSuccess { failure = st }
        }
        if !s.embeddingKey.isEmpty {
            let st = LocalKeychain.set(s.embeddingKey, for: LocalKeychain.embeddingKeyAccount)
            if st != errSecSuccess { failure = st }
        }
        let defaults = UserDefaults.standard
        defaults.set(s.llmModel, forKey: modelDefaultsKey + envSuffix)
        defaults.set(s.llmBaseURL, forKey: baseURLDefaultsKey + envSuffix)
        return failure
    }

    static func clear() {
        LocalKeychain.delete(LocalKeychain.llmKeyAccount)
        LocalKeychain.delete(LocalKeychain.embeddingKeyAccount)
        let defaults = UserDefaults.standard
        defaults.removeObject(forKey: modelDefaultsKey + envSuffix)
        defaults.removeObject(forKey: baseURLDefaultsKey + envSuffix)
    }
}
#endif
