import XCTest
@testable import ActivityCore

final class ActivityInputStateTests: XCTestCase {

    func testOpensAndClosesSegment() {
        var state = ActivityInputState()
        let t0 = Date()
        let started = state.keystroke(charCount: 3, isDelete: false, now: t0)
        XCTAssertEqual(started?.type, "client.input.active")
        XCTAssertNotNil(started?.pairId)

        // 距最后输入不足 5 秒：不收段
        XCTAssertNil(state.tick(now: t0.addingTimeInterval(3)))
        // 续段的第二次输入（t0+3）
        XCTAssertNil(state.keystroke(charCount: 1, isDelete: false, now: t0.addingTimeInterval(3)))

        // 越过 5 秒：收段，时长只算首键→末键
        let t1 = t0.addingTimeInterval(8)
        let ended = state.tick(now: t1)
        XCTAssertEqual(ended?.type, "client.input.ended")
        XCTAssertEqual(ended?.pairId, started?.pairId, "区间配对靠 pair_id 互指")
        XCTAssertEqual(ended?.attrs?["duration_ms"], .number(3000), "末键在 t0+3，时长 3s，不含后面静默")
        XCTAssertEqual(ended?.attrs?["chars_bucket"], .string("0-50"))
        XCTAssertEqual(ended?.attrs?["had_delete"], .bool(false))
    }

    func testAccumulatesAndBuckets() {
        var state = ActivityInputState()
        let t0 = Date()
        _ = state.keystroke(charCount: 1, isDelete: false, now: t0)
        _ = state.keystroke(charCount: 60, isDelete: false, now: t0.addingTimeInterval(1))
        _ = state.keystroke(charCount: 200, isDelete: true, now: t0.addingTimeInterval(2))
        let ended = state.end(now: t0.addingTimeInterval(3))
        XCTAssertEqual(ended?.attrs?["chars_bucket"], .string("201+"))
        XCTAssertEqual(ended?.attrs?["had_delete"], .bool(true))
    }

    func testEndClosesImmediately() {
        var state = ActivityInputState()
        let t0 = Date()
        _ = state.keystroke(charCount: 1, isDelete: false, now: t0)
        let ended = state.end(now: t0.addingTimeInterval(0.5))
        XCTAssertEqual(ended?.type, "client.input.ended")
        XCTAssertNil(state.end(now: t0), "空段不重复收")
    }

    func testBuckets() {
        XCTAssertEqual(ActivityInputState.bucket(0), "0-50")
        XCTAssertEqual(ActivityInputState.bucket(50), "0-50")
        XCTAssertEqual(ActivityInputState.bucket(51), "51-200")
        XCTAssertEqual(ActivityInputState.bucket(200), "51-200")
        XCTAssertEqual(ActivityInputState.bucket(201), "201+")
    }
}
