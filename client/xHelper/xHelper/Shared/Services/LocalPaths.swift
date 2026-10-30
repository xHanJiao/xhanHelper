import Foundation

/// 本地模式的分环境路径/端口（第 4 期）：**Debug 构建 = dev 沙盒，Release 构建 = prod 真身**。
///
/// 单一事实来源 —— 数据根 / 后端日志根 / 端口 / 环境标签都从这里取，
/// **禁止再在别处写字面量 `"xhanHelper"`**（散落的字面量正是 dev/prod 串味的来源：
/// favicons / history / tool_output 会写进同一个根）。
///
/// 与 Go 侧的四者配套断言（`appinit.AssertLocalEnv`）互为镜像：
/// `local_env` ↔ `APP_ENV` ↔ 数据根 ↔ token 前缀。
/// iOS 也编这份文件（连云端用不到本地路径），所以不套 `#if os(macOS)`。
enum LocalPaths {

    /// 环境标签。写进本地 token 前缀（`<env>:<random>`），后端据此校验，不符 403。
    static var env: String {
        #if DEBUG
        "dev"
        #else
        "prod"
        #endif
    }

    static var isDev: Bool { env == "dev" }

    /// Go 侧的 `APP_ENV`：dev → `local.dev`，prod → `local`。
    static var appEnv: String { isDev ? "local.dev" : "local" }

    /// 后端配置文件名（打进 app 的 Contents/Resources/conf/，由 bundle-backend.sh 按构建配置选）。
    static var confFileName: String { isDev ? "config.local.dev.yaml" : "config.local.yaml" }

    /// 本地后端端口。
    static var backendPort: Int { isDev ? 18924 : 18923 }

    /// 两个环境的数据根目录名（单一来源；「禁止再写字面量 `xhanHelper`」）。
    static let devDataRootName = "xhanHelper-dev"
    static let prodDataRootName = "xhanHelper"

    /// 数据根目录名（`~/Library/Application Support/<name>/`）。
    static var dataRootName: String { isDev ? devDataRootName : prodDataRootName }

    /// 所有环境的数据根名（护栏用：如「备份位置不得选进任一环境数据根」）。
    static var allDataRootNames: [String] { [devDataRootName, prodDataRootName] }

    /// 日志目录名（`~/Library/Logs/<name>/`）。
    static var logRootName: String { isDev ? "xhanHelper-dev" : "xhanHelper" }

    private static var appSupportDir: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
    }

    private static var libraryDir: URL {
        FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
    }

    /// 数据根（库 / images / favicons / *.json / tool_output / local_results 都在它下面）。
    static var dataRoot: URL {
        appSupportDir.appendingPathComponent(dataRootName, isDirectory: true)
    }

    /// 两个环境的数据根（护栏用，顺序同 `allDataRootNames`）。
    static var allDataRoots: [URL] {
        allDataRootNames.map { appSupportDir.appendingPathComponent($0, isDirectory: true) }
    }

    /// 后端（Go 子进程）日志目录。
    static var backendLogDir: URL { libraryDir.appendingPathComponent("Logs/\(logRootName)", isDirectory: true) }

    /// app 的 Caches 根（`~/Library/Caches/com.xhan.xhanHelper/`；dev/prod 靠子目录后缀分开）。
    static var appCachesRoot: URL {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
        return caches.appendingPathComponent("com.xhan.xhanHelper", isDirectory: true)
    }

    /// Swift 侧 app 日志目录（Caches 下；dev/prod 分开，免得两个 app 的日志混在一起）。
    static var appLogDir: URL {
        appCachesRoot.appendingPathComponent("logs\(isDev ? "-dev" : "")")
    }

    /// 灵阅正文缓存目录（`ArtifactHTMLCache`；Caches 下，dev/prod 分开）。
    static var artifactsCacheDir: URL {
        appCachesRoot.appendingPathComponent(isDev ? "artifacts-dev" : "artifacts", isDirectory: true)
    }

    /// 给 app 级配置 key 加环境后缀：dev/prod 共用同一 bundle id 的 UserDefaults 域，
    /// 不加后缀时 dev 里设的值会串到 prod（数据根都分开了，配置同理）。
    static func scoped(_ key: String) -> String { "\(key)_\(env)" }

    /// 用户填的「服务地址」`xhan_server_url`（已 trim；空白/未填 = nil）。判空口径的唯一来源。
    static var serverURLOverride: String? { trimmedOverride("xhan_server_url") }
    /// 用户填的「网页地址」`xhan_web_url`（已 trim；空白/未填 = nil）。
    static var webURLOverride: String? { trimmedOverride("xhan_web_url") }

    private static func trimmedOverride(_ key: String) -> String? {
        guard let raw = UserDefaults.standard.string(forKey: scoped(key)) else { return nil }
        let t = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : t
    }
}

/// 「使用本地后端」开关的 key 与默认值（唯一事实来源）。
///
/// 默认一律关（dev/prod 一致）：默认连 `DeviceManager.defaultBaseURL`
/// （Debug=localhost:8080 / Release=云端），只有显式打开才用 app 自启动的内嵌后端。
/// 仅在未填 `xhan_server_url` 时生效（填了地址一律当远端，见 `LocalBackendManager.isActive`）。
enum LocalBackendPreference {
    static var useLocalKey: String { LocalPaths.scoped("xhan_use_local_backend") }
    static let useLocalDefault = false
}

/// 「久等回复完成」提示音的 key 与默认值（唯一事实来源，避免设置页与读取端各写一份漂移）。
///
/// key 带环境后缀（见 `LocalPaths.scoped`）。默认阈值随构建区分：dev 便于测试取 5s，prod 取 180s。
enum LongReplyAlert {
    static var soundKey: String { LocalPaths.scoped("xhan_long_reply_alert_sound") }
    static var secondsKey: String { LocalPaths.scoped("xhan_long_reply_alert_seconds") }
    static let defaultSoundEnabled = true
    static var defaultSeconds: Int { LocalPaths.isDev ? 5 : 180 }
}
