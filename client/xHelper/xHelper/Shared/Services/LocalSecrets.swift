import Foundation

/// 本地后端的密钥 + 可选覆盖项（纯数据 + 校验，便于 `swift test`）。
///
/// provider 锁死 `deepseek`（不暴露）；embedding 只放开 key（base_url / model 锁死）。
/// 两个 key 存 Keychain（见 `LocalKeychain`）；model / base_url 只是可选项，空 = 用后端默认。
struct LocalSecrets: Equatable {
    /// DeepSeek LLM key（必需）
    var llmKey: String = ""
    /// Ark Embedding key（必需）
    var embeddingKey: String = ""
    /// 可改：LLM 模型（deepseek 家族）。空 = 后端默认。
    var llmModel: String = ""
    /// 可改：LLM base_url。空 = 后端默认。
    var llmBaseURL: String = ""

    static let defaultLLMModel = "deepseek-flash"
    static let defaultLLMBaseURL = "https://api.deepseek.com"

    /// 后端 `conf/config.yaml` 里 embedding 的默认值（本地不放开，仅供「测试连接」用）。
    static let embeddingBaseURL = "https://ark.cn-beijing.volces.com"
    static let embeddingModel = "doubao-embedding-vision-251215"

    /// 两个 key 都非空才算配好（少一个后端起得来，但 embedding 会在检索时失败）。
    var isComplete: Bool {
        !llmKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !embeddingKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    /// 归一化：去首尾空白；可选项为空时保持空（表示「用默认」）。
    func normalized() -> LocalSecrets {
        let t = { (s: String) in s.trimmingCharacters(in: .whitespacesAndNewlines) }
        return LocalSecrets(
            llmKey: t(llmKey),
            embeddingKey: t(embeddingKey),
            llmModel: t(llmModel),
            llmBaseURL: t(llmBaseURL)
        )
    }

    /// 拼接的 base_url（空则回退默认）——供「测试连接」用。
    var effectiveLLMBaseURL: String {
        llmBaseURL.isEmpty ? Self.defaultLLMBaseURL : llmBaseURL
    }

    /// 实际使用的模型名（空则回退默认）——仅供 UI 展示。
    var effectiveLLMModel: String {
        llmModel.isEmpty ? Self.defaultLLMModel : llmModel
    }
}
