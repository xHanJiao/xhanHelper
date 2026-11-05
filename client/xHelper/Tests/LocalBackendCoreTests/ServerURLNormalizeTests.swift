import XCTest
@testable import LocalBackendCore

/// 用户手填服务地址的归一化。
/// 回归背景：`127.0.0.1:8080` 这类不带 scheme 的写法会让 `URL(string:)` 返回 nil，
/// 请求层当年用 `!` 强解包 → 启动请求直接崩。
final class ServerURLNormalizeTests: XCTestCase {

    func testSchemeLessPrivateHostGetsHTTP() {
        XCTAssertEqual(LocalPaths.normalizeServerURL("127.0.0.1:8080"), "http://127.0.0.1:8080")
        XCTAssertEqual(LocalPaths.normalizeServerURL("localhost:8080/"), "http://localhost:8080")
        XCTAssertEqual(LocalPaths.normalizeServerURL("192.168.1.5:18923"), "http://192.168.1.5:18923")
        XCTAssertEqual(LocalPaths.normalizeServerURL("10.0.0.8:8080"), "http://10.0.0.8:8080")
        XCTAssertEqual(LocalPaths.normalizeServerURL("172.20.3.4:8080"), "http://172.20.3.4:8080")
        XCTAssertEqual(LocalPaths.normalizeServerURL("mac-mini.local:8080"), "http://mac-mini.local:8080")
    }

    func testSchemeLessPublicHostGetsHTTPS() {
        // 公网主机补 https：避免首次登录明文发密码（也避免 80→443 的 301 把 POST 改写）
        XCTAssertEqual(LocalPaths.normalizeServerURL("helper.xhanx.cn"), "https://helper.xhanx.cn")
        XCTAssertEqual(LocalPaths.normalizeServerURL("8.8.8.8:9000"), "https://8.8.8.8:9000")
        XCTAssertEqual(LocalPaths.normalizeServerURL("fc-host.example.com"), "https://fc-host.example.com")
    }

    func testExplicitSchemeKeptAndTrailingSlashTrimmed() {
        XCTAssertEqual(LocalPaths.normalizeServerURL("https://helper.xhanx.cn/"), "https://helper.xhanx.cn")
        XCTAssertEqual(LocalPaths.normalizeServerURL("  http://127.0.0.1:18923//  "), "http://127.0.0.1:18923")
    }

    func testEmptyIsNil() {
        XCTAssertNil(LocalPaths.normalizeServerURL(""))
        XCTAssertNil(LocalPaths.normalizeServerURL("   "))
    }

    func testInvalidHostReturnedAsIsForVisibleError() {
        // 不静默回退默认后端：原样返回，让请求层抛「服务地址无效」
        XCTAssertEqual(LocalPaths.normalizeServerURL("http://[bad"), "http://[bad")
    }

    func testNormalizedValueProducesValidURL() throws {
        for raw in ["127.0.0.1:8080", "localhost:18923", "helper.xhanx.cn/"] {
            let normalized = try XCTUnwrap(LocalPaths.normalizeServerURL(raw))
            XCTAssertNotNil(URL(string: normalized + "/api/chat"), "归一化后必须能构造请求 URL：\(raw)")
        }
    }

    func testPrivateHostClassification() {
        for host in ["127.0.0.1", "localhost", "10.1.2.3", "172.16.0.1", "172.31.255.254",
                     "192.168.0.1", "169.254.1.1", "100.64.0.1", "100.127.255.254",
                     "198.18.0.1", "198.19.255.254", "::1", "fd00::1", "fe80::1", "febf::1", "nas.local"] {
            XCTAssertTrue(LocalPaths.isLocalOrPrivateHost(host), "应判为私网：\(host)")
        }
        for host in ["helper.xhanx.cn", "8.8.8.8", "172.32.0.1", "192.169.0.1", "100.128.0.1",
                     "198.20.0.1", "fec0::1", "fckeditor.example.com"] {
            XCTAssertFalse(LocalPaths.isLocalOrPrivateHost(host), "应判为公网：\(host)")
        }
    }
}
