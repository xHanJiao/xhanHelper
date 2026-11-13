import XCTest
@testable import BrowserEngineCore

/// cookie 域/路径/secure 匹配是「下载带登录态」与「不跨站泄露」的同一条线，逐条钉住。
final class BrowserCookieMatchingTests: XCTestCase {

    private func cookie(_ name: String, domain: String, path: String = "/", secure: Bool = false) -> HTTPCookie {
        HTTPCookie(properties: [
            .name: name, .value: "v", .domain: domain, .path: path,
            .secure: secure ? "TRUE" : nil,
        ].compactMapValues { $0 })!
    }

    private func url(_ s: String) -> URL { URL(string: s)! }

    func testHostAndSubdomainMatch() {
        let all = [cookie("a", domain: "example.com"), cookie("b", domain: ".example.com")]
        XCTAssertEqual(BrowserCookieMatching.matching(all, for: url("https://example.com/x")).count, 2)
        XCTAssertEqual(BrowserCookieMatching.matching(all, for: url("https://api.example.com/x")).count, 2)
        // 后缀伪装不能命中
        XCTAssertTrue(BrowserCookieMatching.matching(all, for: url("https://example.com.evil/x")).isEmpty)
        XCTAssertTrue(BrowserCookieMatching.matching(all, for: url("https://notexample.com/x")).isEmpty)
    }

    func testSecureCookieOnlyOverHTTPS() {
        let all = [cookie("s", domain: "example.com", secure: true)]
        XCTAssertEqual(BrowserCookieMatching.matching(all, for: url("https://example.com/")).count, 1)
        XCTAssertTrue(BrowserCookieMatching.matching(all, for: url("http://example.com/")).isEmpty)
    }

    func testPathPrefixMatch() {
        let all = [cookie("root", domain: "example.com", path: "/"),
                   cookie("video", domain: "example.com", path: "/video")]
        let names = BrowserCookieMatching.matching(all, for: url("https://example.com/video/1.mp4")).map(\.name)
        XCTAssertEqual(names, ["root", "video"])
        // 路径前缀必须落在 "/" 边界上
        let other = BrowserCookieMatching.matching(all, for: url("https://example.com/videofeed")).map(\.name)
        XCTAssertEqual(other, ["root"])
    }

    func testEmptyPathTreatedAsRoot() {
        let c = HTTPCookie(properties: [.name: "x", .value: "v", .domain: "example.com", .path: "/"])!
        XCTAssertEqual(BrowserCookieMatching.matching([c], for: url("https://example.com/a/b")).count, 1)
    }

    func testNilHostReturnsEmpty() {
        let all = [cookie("a", domain: "example.com")]
        XCTAssertTrue(BrowserCookieMatching.matching(all, for: URL(string: "file:///tmp/x")!).isEmpty)
    }
}
