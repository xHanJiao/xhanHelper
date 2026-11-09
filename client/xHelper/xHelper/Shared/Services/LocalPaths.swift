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

    /// 用户填的「服务地址」`xhan_server_url`（已归一化；空白/未填 = nil）。判空口径的唯一来源。
    static var serverURLOverride: String? {
        guard let raw = trimmedOverride("xhan_server_url") else { return nil }
        return normalizeServerURL(raw)
    }
    /// 用户填的「网页地址」`xhan_web_url`（已 trim；空白/未填 = nil）。
    static var webURLOverride: String? { trimmedOverride("xhan_web_url") }

    private static func trimmedOverride(_ key: String) -> String? {
        guard let raw = UserDefaults.standard.string(forKey: scoped(key)) else { return nil }
        let t = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : t
    }

    /// 归一化用户填写的服务地址：去首尾空白与尾斜杠，缺 scheme 时按主机补
    /// （回环 / 私网 → `http://`，其余 → `https://`）。
    ///
    /// 为什么必须做：`URL(string: "127.0.0.1:8080/api/chat")` 返回 nil，而请求层曾用 `!` 强解包，
    /// 用户填一个不带 scheme 的本机地址（最常见写法）就会在启动请求时崩溃。
    ///
    /// 为什么远端补 https：给公网主机补 http 会把首次登录的密码明文发出去；
    /// 线上 nginx 的 80→443 301 还会把 POST 改写成 GET，表现为「登录失败」而不是「地址无效」。
    /// 用户确实要明文时显式写 `http://` 即可。
    ///
    /// 归一化后仍解析不出 host 时**原样返回**（不静默回退默认后端）——由请求层抛出可见的
    /// 「服务地址无效」，避免用户以为连上了另一个后端。
    static func normalizeServerURL(_ raw: String) -> String? {
        var t = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return nil }
        if !t.contains("://") {
            let host = URLComponents(string: "http://" + t)?.host ?? ""
            t = (isLocalOrPrivateHost(host) ? "http" : "https") + "://" + t
        }
        if let schemeRange = t.range(of: "://") {
            let prefixCount = t.distance(from: t.startIndex, to: schemeRange.upperBound)
            while t.count > prefixCount, t.hasSuffix("/") { t.removeLast() }
        }
        guard let components = URLComponents(string: t),
              let host = components.host, !host.isEmpty else {
            return raw.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        return t
    }

    /// 是否本机 / 局域网主机（这些补 http；公网补 https）。
    /// `.local`（mDNS）也按局域网处理。
    static func isLocalOrPrivateHost(_ rawHost: String) -> Bool {
        let host = rawHost.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
        guard !host.isEmpty else { return false }
        if host == "localhost" || host == "::1" || host.hasSuffix(".local") { return true }
        if host.contains(":") {
            // IPv6：ULA fc00::/7 与 link-local fe80::/10（fe80–febf，不只是 fe80）
            if host.hasPrefix("fc") || host.hasPrefix("fd") { return true }
            return ["fe8", "fe9", "fea", "feb"].contains { host.hasPrefix($0) }
        }
        let parts = host.split(separator: ".").compactMap { Int($0) }
        guard parts.count == 4, host.split(separator: ".").count == 4,
              parts.allSatisfy({ (0...255).contains($0) }) else { return false }
        let a = parts[0], b = parts[1]
        return a == 0 || a == 127 || a == 10
            || (a == 172 && (16...31).contains(b))
            || (a == 192 && b == 168)
            || (a == 169 && b == 254)
            || (a == 100 && (64...127).contains(b))   // CGNAT / Tailscale 100.64/10
            || (a == 198 && (18...19).contains(b))    // 基准测试网段 198.18/15（也常用于内网）
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

/// 语音音色偏好（本地保存；空 = 没选过，用服务端默认）。
enum VoicePreference {
    static var speakerKey: String { LocalPaths.scoped("xhan_voice_speaker") }
    /// 近场电平闸档位（off / loose / standard / near）。默认 standard。
    static var nearFieldKey: String { LocalPaths.scoped("xhan_voice_nearfield") }
    static let nearFieldDefault = "standard"
}
