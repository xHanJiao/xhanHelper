import XCTest
@testable import ActivityCore

final class ActivityPresenceStateTests: XCTestCase {

    func testLaunchedAndBoundaryAreRootClientEvents() {
        let state = ActivityPresenceState()
        let launched = state.launched()
        XCTAssertEqual(launched.type, "client.app.launched")
        XCTAssertEqual(launched.actor, "system")
        XCTAssertNil(launched.parentEventId, "在场事件是根，父必须为空")

        let boundary = state.boundary()
        XCTAssertEqual(boundary.type, "system.boundary")
        XCTAssertEqual(boundary.attrs?["source"], .string("client"))
    }

    func testFocusEpochRotatesPerActivation() {
        var state = ActivityPresenceState()
        XCTAssertNil(state.focusEpochId)

        let gained = state.focusGained()
        XCTAssertEqual(gained.type, "client.focus.gained")
        let first = gained.focusEpochId
        XCTAssertNotNil(first)

        let lost = state.focusLost()
        XCTAssertEqual(lost?.type, "client.focus.lost")
        XCTAssertEqual(lost?.focusEpochId, first, "失活必须用激活时那个 epoch")

        _ = state.focusGained()
        XCTAssertNotEqual(state.focusEpochId, first, "再次激活要开新 epoch")
    }

    func testFocusLostWithoutGainedProducesNothing() {
        var state = ActivityPresenceState()
        XCTAssertNil(state.focusLost(), "没有已知 epoch 就不硬造区间")
    }

    func testFocusLostTwiceProducesOnlyOnce() {
        var state = ActivityPresenceState()
        _ = state.focusGained()
        XCTAssertNotNil(state.focusLost())
        XCTAssertNil(state.focusLost(), "同一次激活被失活两次只落一条 lost")
    }

    func testQuitScreensAndPowerEvents() {
        let state = ActivityPresenceState()
        XCTAssertEqual(state.quit().type, "client.app.quit")
        XCTAssertEqual(state.screensSlept().type, "system.screens.sleep")
        XCTAssertEqual(state.screensWoke().type, "system.screens.wake")
        XCTAssertEqual(state.powerOff().type, "system.power.off")
    }

    func testIdleStartAndEnd() {
        var state = ActivityPresenceState()
        let t0 = Date(timeIntervalSince1970: 1_000)
        let threshold = ActivityPresenceState.idleThresholdSeconds

        XCTAssertNil(state.idleTick(idleSeconds: 10, now: t0), "未越阈值不发")

        let started = state.idleTick(idleSeconds: threshold, now: t0)
        XCTAssertEqual(started?.type, "client.idle.started")
        XCTAssertEqual(started?.attrs?["threshold_s"], .number(threshold), "记下当时阈值，供将来校准区分")
        XCTAssertNil(state.idleTick(idleSeconds: threshold + 20, now: t0.addingTimeInterval(20)),
                     "同一 idle 段不重复发 started")

        // idle 实际从 t0-300 起（t0 时已 idle 300s），用户 t0+120 回来 → 总时长 420s。
        let ended = state.idleTick(idleSeconds: 0, now: t0.addingTimeInterval(120))
        XCTAssertEqual(ended?.type, "client.idle.ended")
        XCTAssertEqual(ended?.attrs?["idle_seconds"], .number(420))
        XCTAssertNil(state.idleTick(idleSeconds: 0, now: t0.addingTimeInterval(130)), "已结束不重复发")
    }

    func testSleepUsesCurrentWakeEpochAndWakeRotatesIt() {
        var state = ActivityPresenceState()
        let first = state.wakeEpochId
        XCTAssertNotNil(first)

        XCTAssertEqual(state.slept().wakeEpochId, first, "sleep 收尾的是当前 wake epoch")

        let woke = state.woke()
        XCTAssertEqual(woke.type, "system.wake")
        XCTAssertNotEqual(woke.wakeEpochId, first, "唤醒要开新 wake epoch")

        XCTAssertEqual(state.slept().wakeEpochId, woke.wakeEpochId, "下一次 sleep 用新 epoch")
    }
}
