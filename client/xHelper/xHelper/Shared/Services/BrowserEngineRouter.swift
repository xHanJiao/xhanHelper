import Foundation

/// 浏览器内核。`cef` = Chromium（主引擎）；`compat` = WKWebView（视频站 + 手动兜底，D11 永久保留）。
///
/// 引擎在 tab 创建时定死、之后不再变（D15 tab 粘性）——两个引擎的 cookie / 滚动 / 表单状态
/// 无法移植，切一次等于销毁重建。
enum BrowserEngine: String, CaseIterable {
    case cef
    case compat

    var displayName: String { self == .cef ? "Chromium" : "兼容（WebKit）" }
}

/// 「这个页面该用哪个引擎」的**唯一真相来源**（D15~D19）。
///
/// 规则错一次，用户遇到的是"视频放不了 / 怎么又没登录 / agent 动了我在看的页面"，
/// 所以判定、偏好、白名单全部收在这里，view / tool / 收藏历史都不许自己 if 域名。
/// 见 docs/design/mac-apps/browser-chromium-migration.md §9.2。
enum BrowserEngineRouter {

    /// 视频站必须走兼容引擎：CEF 官方二进制不含 H.264/AAC（D12）。
    /// 短链也列进来，否则短链先落 CEF、302 之后才发现开错。
    static let videoHosts: [String] = [
        "bilibili.com", "douyin.com", "xiaohongshu.com",
        "b23.tv", "v.douyin.com", "xhslink.com",
    ]

    /// 白名单是否生效。B2 先关（D-b：B2 期间视频站也走 CEF，靠隐藏开关手动回 WK），**B5 起打开**。
    static var videoWhitelistEnabled = true

    /// 本文件同时编进 SwiftPM 纯逻辑测试包（那边拿不到 `LocalPaths`），所以环境后缀在这里就地取。
    /// **必须与 `LocalPaths.scoped` 同形**（`<key>_<env>`）：dev / prod 分开存，避免串味。
    static var envSuffix: String {
        #if DEBUG
        "dev"
        #else
        "prod"
        #endif
    }

    /// 设置页「浏览器内核」开关（2026-11-13 起对用户露出；此前是 dev 隐藏开关）。
    /// 也可以命令行改：`defaults write xhanx.cn.xHelperMac xhan_browser_engine_dev -string compat`
    static var defaultEngineKey: String { "xhan_browser_engine_" + envSuffix }

    /// 开关值 → 默认引擎。只有 `compat` 表示"回兼容引擎"，其余（含未设置）都走 CEF。
    static var defaultEngine: BrowserEngine {
        UserDefaults.standard.string(forKey: defaultEngineKey) == BrowserEngine.compat.rawValue ? .compat : .cef
    }

    static func isVideoHost(_ host: String?) -> Bool {
        guard let host = host?.lowercased() else { return false }
        return videoHosts.contains { host == $0 || host.hasSuffix("." + $0) }
    }

    /// 判定顺序（§9.2.2）：
    /// 1 非 http(s) → compat；2 显式指定 → 用它；3 跟 opener；
    /// 4 域名偏好；5 视频白名单 → compat；6 默认引擎。
    ///
    /// - Parameter cefAvailable: CEF 起不来（缺 framework / Initialize 失败）时传 false，整体降级兼容引擎。
    static func resolveEngine(url: URL?,
                              override: BrowserEngine? = nil,
                              openerEngine: BrowserEngine? = nil,
                              cefAvailable: Bool = true) -> BrowserEngine {
        // 1. 本地文件 / PDF / about: 一律兼容引擎（不需要 Chrome 身份，且 CEF 的 PDF 能力未验证）
        let scheme = url?.scheme?.lowercased()
        if let scheme, scheme != "http", scheme != "https" { return .compat }
        if url == nil { return cefAvailable ? defaultEngine : .compat }

        // 2. agent / 用户显式指定
        if let override { return override }
        // 3. popup / window.open 跟 opener（跨引擎会断 window.opener + cookie 域，登录弹窗必挂）
        if let openerEngine { return openerEngine }
        // 4. 域名偏好（用户上次手动选过）
        if let preferred = EnginePreferenceStore.engine(for: url?.host) { return preferred }
        // 5. 视频站白名单
        if videoWhitelistEnabled, isVideoHost(url?.host) { return .compat }
        // 6. 默认
        if !cefAvailable { return .compat }
        return defaultEngine
    }
}

/// 按 eTLD+1 记住用户手动选过的引擎（D15 第 4 条：别烦用户）。
///
/// shortcut: 域名归并取"最后两段"，`a.co.uk` / `a.com.cn` 这类会被切开——B5 接偏好 UI 时
/// 若真碰到，再换成公共后缀表；现在没有写入入口（B5 才写），不影响行为。
enum EnginePreferenceStore {
    private static var key: String { "xhan_engine_preferences_" + BrowserEngineRouter.envSuffix }

    static func hostKey(_ host: String?) -> String? {
        guard let host = host?.lowercased(), !host.isEmpty else { return nil }
        let parts = host.split(separator: ".")
        guard parts.count > 2 else { return host }
        return parts.suffix(2).joined(separator: ".")
    }

    static func engine(for host: String?) -> BrowserEngine? {
        guard let k = hostKey(host),
              let raw = (UserDefaults.standard.dictionary(forKey: key) as? [String: String])?[k] else { return nil }
        return BrowserEngine(rawValue: raw)
    }

    static func setEngine(_ engine: BrowserEngine?, for host: String?) {
        guard let k = hostKey(host) else { return }
        var all = (UserDefaults.standard.dictionary(forKey: key) as? [String: String]) ?? [:]
        if let engine {
            all[k] = engine.rawValue
        } else {
            all.removeValue(forKey: k)
        }
        UserDefaults.standard.set(all, forKey: key)
    }
}

/// 跨引擎提示的频率闸（§9.2.3）：同一 host **24h 内最多提示 2 次**。
///
/// 纯逻辑 + 显式 `now`（可单测）；存储与偏好表同形（按 eTLD+1、带环境后缀）。
/// 只负责"还能不能弹"，弹过之后由调用方 `record`。
enum EngineHintGate {
    static let maxPerWindow = 2
    static let window: TimeInterval = 24 * 3600

    private static var key: String { "xhan_engine_hint_" + BrowserEngineRouter.envSuffix }

    /// host → [上次弹出的 epoch 秒, 窗口内已弹次数]
    private static func load() -> [String: [String: Double]] {
        (UserDefaults.standard.dictionary(forKey: key) as? [String: [String: Double]]) ?? [:]
    }

    private static func count(_ entry: [String: Double]?, now: Date) -> Int {
        guard let last = entry?["t"], now.timeIntervalSince1970 - last < window else { return 0 }
        return Int(entry?["n"] ?? 0)
    }

    /// 现在还能不能对该 host 提示（只读，不记账）
    static func canShow(host: String?, now: Date = Date()) -> Bool {
        guard let k = EnginePreferenceStore.hostKey(host) else { return false }
        return count(load()[k], now: now) < maxPerWindow
    }

    /// 真的弹了才记账（窗口过期自动从 0 计）
    static func record(host: String?, now: Date = Date()) {
        guard let k = EnginePreferenceStore.hostKey(host) else { return }
        var all = load()
        let used = count(all[k], now: now)
        all[k] = ["t": now.timeIntervalSince1970, "n": Double(used + 1)]
        UserDefaults.standard.set(all, forKey: key)
    }

    /// 测试/调试用：清掉全部计数
    static func reset() { UserDefaults.standard.removeObject(forKey: key) }
}
