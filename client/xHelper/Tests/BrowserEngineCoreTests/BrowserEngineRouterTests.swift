import XCTest
@testable import BrowserEngineCore

final class BrowserEngineRouterTests: XCTestCase {

    func testNonHTTPSchemeFallsBackToCompat() {
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: URL(string: "file:///tmp/a.pdf")), .compat)
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: URL(string: "about:blank")), .compat)
    }

    func testExplicitOverrideWinsOverWhitelist() {
        // agent 显式要 cef 的 bilibili 页（D19）
        let url = URL(string: "https://www.bilibili.com/video/av1")!
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: url, override: .cef), .cef)
    }

    func testOpenerEngineIsInheritedEvenForVideoHost() {
        // D17：弹窗跟 opener，即使目标是白名单域名
        let url = URL(string: "https://www.bilibili.com/")!
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: url, openerEngine: .cef), .cef)
    }

    func testVideoHostsMatchSubdomainAndShortLinkOnly() {
        XCTAssertTrue(BrowserEngineRouter.isVideoHost("www.bilibili.com"))
        XCTAssertTrue(BrowserEngineRouter.isVideoHost("b23.tv"))
        XCTAssertFalse(BrowserEngineRouter.isVideoHost("notbilibili.com"))
        XCTAssertFalse(BrowserEngineRouter.isVideoHost(nil))
    }

    func testWhitelistKeepsVideoOnCompatWhenEnabled() {
        let original = BrowserEngineRouter.videoWhitelistEnabled
        defer { BrowserEngineRouter.videoWhitelistEnabled = original }
        let url = URL(string: "https://m.douyin.com/x")!
        BrowserEngineRouter.videoWhitelistEnabled = false
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: url), .cef)   // B2 阶段白名单未生效（D-b）
        BrowserEngineRouter.videoWhitelistEnabled = true
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: url), .compat)
    }

    func testVideoHostGoesCompatByDefault() {
        // B5 起白名单默认生效：视频站（含短链）不配置任何东西就落兼容引擎
        XCTAssertTrue(BrowserEngineRouter.videoWhitelistEnabled)
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: URL(string: "https://www.bilibili.com/video/BV1")!), .compat)
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: URL(string: "https://b23.tv/abc")!), .compat)
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: URL(string: "https://www.xiaohongshu.com/x")!), .compat)
    }

    func testHintGateAllowsTwicePerDay() {
        EngineHintGate.reset()
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let host = "www.bilibili.com"
        XCTAssertTrue(EngineHintGate.canShow(host: host, now: now))
        EngineHintGate.record(host: host, now: now)
        EngineHintGate.record(host: host, now: now)
        // 24h 内最多 2 次
        XCTAssertFalse(EngineHintGate.canShow(host: host, now: now.addingTimeInterval(3600)))
        // 子域并到同一个 eTLD+1 计数
        XCTAssertFalse(EngineHintGate.canShow(host: "m.bilibili.com", now: now.addingTimeInterval(3600)))
        // 过了窗口重新计数
        XCTAssertTrue(EngineHintGate.canShow(host: host, now: now.addingTimeInterval(EngineHintGate.window + 1)))
        EngineHintGate.reset()
    }

    func testCefUnavailableFallsBackToCompat() {
        let url = URL(string: "https://www.google.com/")!
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: url, cefAvailable: false), .compat)
    }

    func testHostKeyMergesSubdomains() {
        XCTAssertEqual(EnginePreferenceStore.hostKey("pan.baidu.com"), "baidu.com")
        XCTAssertEqual(EnginePreferenceStore.hostKey("baidu.com"), "baidu.com")
        XCTAssertNil(EnginePreferenceStore.hostKey(nil))
    }

    // MARK: - 空白 tab 首次导航复算（D15 补充规则）

    func testBlankTabFirstNavigationAdoptsSiteRule() {
        let original = BrowserEngineRouter.videoWhitelistEnabled
        defer { BrowserEngineRouter.videoWhitelistEnabled = original }
        BrowserEngineRouter.videoWhitelistEnabled = true
        // 默认 CEF 起的空 tab，点小红书磁贴 / 地址栏输小红书 → 换成兼容引擎
        XCTAssertEqual(BrowserEngineRouter.rerouteTarget(
            current: .cef, url: URL(string: "https://www.xiaohongshu.com/explore/1")!), .compat)
        // 普通站不换（否则每开一个空白页都多一张 tab）
        XCTAssertNil(BrowserEngineRouter.rerouteTarget(
            current: .cef, url: URL(string: "https://www.baidu.com/")!))
    }

    func testBlankTabFirstNavigationFollowsEnginePreference() {
        EnginePreferenceStore.reset()
        defer { EnginePreferenceStore.reset() }
        EnginePreferenceStore.setEngine(.compat, for: "example-pref.test")
        XCTAssertEqual(BrowserEngineRouter.rerouteTarget(
            current: .cef, url: URL(string: "https://www.example-pref.test/a")!), .compat)
    }

    func testBlankTabRerouteSkipsCefWhenEngineUnavailable() {
        EnginePreferenceStore.reset()
        defer { EnginePreferenceStore.reset() }
        let url = URL(string: "https://www.example-cef-pref.test/a")!
        EnginePreferenceStore.setEngine(.cef, for: url.host)
        // 主引擎起不来：偏好写着 cef 也不能把 compat 换过去（换完又降级，来回换）
        XCTAssertNil(BrowserEngineRouter.rerouteTarget(current: .compat, url: url, cefUsable: false))
        XCTAssertEqual(BrowserEngineRouter.rerouteTarget(current: .compat, url: url), .cef)
    }

    func testBlankTabRerouteIgnoresNonHTTPSchemes() {
        XCTAssertNil(BrowserEngineRouter.rerouteTarget(current: .cef, url: URL(string: "about:blank")!))
        XCTAssertNil(BrowserEngineRouter.rerouteTarget(current: .cef, url: URL(string: "file:///tmp/a.pdf")!))
    }
}
