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

    /// 数据根目录名（`~/Library/Application Support/<name>/`）。
    static var dataRootName: String { isDev ? "xhanHelper-dev" : "xhanHelper" }

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

    /// 后端（Go 子进程）日志目录。
    static var backendLogDir: URL { libraryDir.appendingPathComponent("Logs/\(logRootName)", isDirectory: true) }

    /// Swift 侧 app 日志目录（Caches 下；dev/prod 分开，免得两个 app 的日志混在一起）。
    static var appLogDir: URL {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
        return caches.appendingPathComponent("com.xhan.xhanHelper/logs\(isDev ? "-dev" : "")")
    }
}
