import XCTest
@testable import ActivityCore

final class ActivityBrowserStateTests: XCTestCase {

    func testNavStartAndFinish() {
        var s = ActivityBrowserState(tabId: "browser-1")
        let t0 = Date()
        let start = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: false, now: t0)
        XCTAssertEqual(start.map(\.type), ["browser.nav.started"])
        XCTAssertEqual(start[0].actor, "user")
        XCTAssertEqual(start[0].pairId, start[0].pairId)

        let finish = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: true, now: t0.addingTimeInterval(1))
        XCTAssertEqual(finish.map(\.type), ["browser.nav.ended"])
        XCTAssertEqual(finish[0].outcome, "ok")
        XCTAssertEqual(finish[0].pairId, start[0].pairId, "起止互指")
    }

    func testPageChangeEmitsDwellThenNewNav() {
        var s = ActivityBrowserState(tabId: "browser-1")
        let t0 = Date()
        _ = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: false, now: t0)
        _ = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: true, now: t0.addingTimeInterval(1))
        let out = s.urlChanged(url: "https://b.com/y", host: "b.com", owner: nil, finished: false, now: t0.addingTimeInterval(11))
        XCTAssertEqual(out.map(\.type), ["browser.page.dwell", "browser.nav.started"])
        XCTAssertEqual(out[0].attrs?["url"], .string("https://a.com/x"))
        XCTAssertEqual(out[0].attrs?["dwell_ms"], .number(11000))
    }

    func testDuplicateFinishIgnored() {
        var s = ActivityBrowserState(tabId: "browser-1")
        let t0 = Date()
        _ = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: false, now: t0)
        _ = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: true, now: t0)
        let again = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: true, now: t0)
        XCTAssertTrue(again.isEmpty, "同一页重复 didFinish 不该再发 nav.started/ended")
    }

    func testHangingNavClosedAsCancelledOnNextPage() {
        var s = ActivityBrowserState(tabId: "browser-1")
        let t0 = Date()
        _ = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: false, now: t0)
        // 没有 didFinish（SPA），直接跳走
        let out = s.urlChanged(url: "https://b.com/y", host: "b.com", owner: nil, finished: false, now: t0.addingTimeInterval(2))
        XCTAssertEqual(out.map(\.type), ["browser.nav.ended", "browser.page.dwell", "browser.nav.started"])
        XCTAssertEqual(out[0].outcome, "cancelled")
    }

    func testAgentOwnerMapsToAssistant() {
        var s = ActivityBrowserState(tabId: "browser-2")
        let out = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: "main", finished: false)
        XCTAssertEqual(out[0].actor, "assistant")
        XCTAssertEqual(out[0].attrs?["owner"], .string("agent"))
    }

    func testNavFailed() {
        var s = ActivityBrowserState(tabId: "browser-1")
        _ = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: false)
        let fail = s.navFailed(errorType: "NSURLErrorDomain/-1003")
        XCTAssertEqual(fail.map(\.type), ["browser.nav.ended"])
        XCTAssertEqual(fail[0].outcome, "failed")
        XCTAssertEqual(fail[0].errorType, "NSURLErrorDomain/-1003")
    }

    func testClosedEmitsHangingAndDwell() {
        var s = ActivityBrowserState(tabId: "browser-1")
        let t0 = Date()
        _ = s.urlChanged(url: "https://a.com/x", host: "a.com", owner: nil, finished: false, now: t0)
        let out = s.closed(now: t0.addingTimeInterval(4))
        XCTAssertEqual(out.map(\.type), ["browser.nav.ended", "browser.page.dwell"])
    }

    func testNonHttpUrlsIgnored() {
        var s = ActivityBrowserState(tabId: "browser-1")
        XCTAssertTrue(s.urlChanged(url: "about:blank", host: "", owner: nil, finished: true).isEmpty)
        XCTAssertTrue(s.urlChanged(url: "file:///tmp/x.html", host: "", owner: nil, finished: true).isEmpty)
    }

    func testBookmarkAdded() {
        let ev = ActivityBrowserState.bookmarkAdded(url: "https://a.com/x", titleLength: 7)
        XCTAssertEqual(ev.type, "browser.bookmark.added")
        XCTAssertEqual(ev.attrs?["url"], .string("https://a.com/x"))
        XCTAssertEqual(ev.attrs?["title_len"], .number(7))
    }

    func testUrlRedaction() {
        XCTAssertEqual(
            ActivityUrls.sanitized("https://x.com/p?token=abc&q=1#frag"),
            "https://x.com/p?token=REDACTED&q=1")
        XCTAssertEqual(
            ActivityUrls.sanitized("https://x.com/p?a=1"),
            "https://x.com/p?a=1")
        XCTAssertEqual(ActivityUrls.sanitized("https://x.com/p#only-frag"), "https://x.com/p")
    }

    func testWhitelistCoversClientProducers() {
        for t in ["browser.nav.started", "browser.nav.ended", "browser.page.dwell",
                  "browser.bookmark.added", "client.input.active", "client.input.ended"] {
            XCTAssertTrue(ActivityEventTypes.known.contains(t), "附表 A 白名单漏了 \(t)")
        }
        XCTAssertFalse(ActivityEventTypes.known.contains("nope.not.real"))
    }
}
