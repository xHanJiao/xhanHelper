import Foundation

/// LocalBackendManager 的纯逻辑：密钥校验/环境变量拼装 + 子进程退出原因归类。
/// 拆出来是为了能用 SwiftPM `swift test` 自验（进程/网络部分只能在 Xcode 真机验）。
enum LocalBackendDiagnostics {

    /// 返回缺失（或为空）的必需密钥名（人话展示用）。
    static func missingSecrets(_ secrets: LocalSecrets) -> [String] {
        var missing: [String] = []
        if secrets.llmKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            missing.append("DeepSeek LLM Key")
        }
        if secrets.embeddingKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            missing.append("Ark Embedding Key")
        }
        return missing
    }

    /// 拼子进程环境变量：在 app 自身环境上追加本地模式所需的键 + 密钥 + 可选覆盖项。
    /// 密钥来自 Keychain（经 `LocalSecrets`）；可选覆盖项为空则不传，让后端用 YAML 默认。
    static func childEnvironment(
        base: [String: String],
        confDir: String,
        appEnv: String,
        token: String,
        secrets: LocalSecrets,
        backup: LocalBackupSettings.Values = LocalBackupSettings.load()
    ) -> [String: String] {
        let s = secrets.normalized()
        var env = base
        env["APP_ENV"] = appEnv
        env["XHAN_CONF_DIR"] = confDir
        env["XHAN_LOCAL_TOKEN"] = token
        env["LLM_API_KEY"] = s.llmKey
        env["EMBEDDING_API_KEY"] = s.embeddingKey
        if !s.llmModel.isEmpty { env["XHAN_LLM_MODEL"] = s.llmModel }
        if !s.llmBaseURL.isEmpty { env["XHAN_LLM_BASE_URL"] = s.llmBaseURL }
        // 备份设置：改完需「保存并重启后端」才生效（Go 启动时读）
        env["XHAN_BACKUP_ENABLED"] = backup.enabled ? "true" : "false"
        env["XHAN_BACKUP_INTERVAL_DAYS"] = "\(backup.intervalDays)"
        env["XHAN_BACKUP_DAILY_KEEP"] = "\(backup.dailyKeep)"
        env["XHAN_BACKUP_WEEKLY_KEEP"] = "\(backup.weeklyKeep)"
        if !backup.dest.isEmpty { env["XHAN_BACKUP_DEST"] = backup.dest }
        return env
    }

    /// 从子进程日志尾部推断人话原因；无法判断时返回 nil（仍会展示原始日志）。
    static func hint(logTail: String) -> String? {
        let lower = logTail.lowercased()
        if lower.contains("address already in use") {
            return "端口已被占用（是不是已经开着 `make local`，或另一个 app 实例？）"
        }
        if lower.contains("llm_api_key") || lower.contains("embedding_api_key")
            || lower.contains("missing required") || lower.contains("invalid config") {
            return "配置校验失败，请检查密钥是否正确。"
        }
        if lower.contains("unable to open database") || lower.contains("database is locked")
            || lower.contains("permission denied") {
            return "本地数据库打不开，请检查数据目录权限。"
        }
        return nil
    }
}
