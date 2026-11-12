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
        let url = URL(string: "https://m.douyin.com/x")!
        BrowserEngineRouter.videoWhitelistEnabled = false
        defer { BrowserEngineRouter.videoWhitelistEnabled = false }
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: url), .cef)   // B2 阶段白名单未生效（D-b）
        BrowserEngineRouter.videoWhitelistEnabled = true
        XCTAssertEqual(BrowserEngineRouter.resolveEngine(url: url), .compat)
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
}
