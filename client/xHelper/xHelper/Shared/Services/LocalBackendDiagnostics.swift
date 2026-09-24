import Foundation

/// LocalBackendManager 的纯逻辑：环境变量校验/拼装 + 子进程退出原因归类。
/// 拆出来是为了能用 SwiftPM `swift test` 自验（进程/网络部分只能在 Xcode 真机验）。
enum LocalBackendDiagnostics {

    /// 启动后端必需的密钥环境变量。
    static let requiredEnvKeys = ["LLM_API_KEY", "EMBEDDING_API_KEY"]

    /// 返回缺失（或为空）的必需环境变量名。
    static func missingEnv(_ env: [String: String]) -> [String] {
        requiredEnvKeys.filter { (env[$0] ?? "").isEmpty }
    }

    /// 拼子进程环境变量：在 app 自身环境上追加本地模式所需的键。
    static func childEnvironment(
        base: [String: String],
        confDir: String,
        token: String
    ) -> [String: String] {
        var env = base
        env["APP_ENV"] = "local"
        env["XHAN_CONF_DIR"] = confDir
        env["XHAN_LOCAL_TOKEN"] = token
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
