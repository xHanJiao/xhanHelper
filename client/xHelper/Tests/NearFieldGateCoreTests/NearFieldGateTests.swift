import XCTest
@testable import NearFieldGateCore

final class NearFieldGateTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 1_700_000_000)

    /// 每 50ms 一帧
    private func frames(_ level: Double, _ n: Int, from start: Date? = nil,
                        gated: Bool = false) -> [NearFieldGate.Frame] {
        let base = start ?? t0
        return (0..<n).map {
            NearFieldGate.Frame(at: base.addingTimeInterval(Double($0) * 0.05),
                                level: level, gated: gated)
        }
    }

    func testThresholds() {
        XCTAssertNil(NearFieldGate.Sensitivity.off.threshold)
        XCTAssertEqual(NearFieldGate.Sensitivity.loose.threshold, 0.02)
        XCTAssertEqual(NearFieldGate.Sensitivity.standard.threshold, 0.05)
        XCTAssertEqual(NearFieldGate.Sensitivity.near.threshold, 0.10)
    }

    func testOffAlwaysAccepts() {
        let now = t0.addingTimeInterval(1.5)
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: frames(0.001, 40),
                                                 sensitivity: .off, now: now))
    }

    func testShortWindowFailsOpen() {
        let now = t0.addingTimeInterval(0.45)
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: frames(0.001, 10),
                                                 sensitivity: .standard, now: now))
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: [], sensitivity: .standard, now: now))
    }

    func testFarFieldRejectedNearFieldAccepted() {
        let now = t0.addingTimeInterval(1.5)
        XCTAssertFalse(NearFieldGate.shouldAccept(frames: frames(0.006, 40),
                                                  sensitivity: .standard, now: now))
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: frames(0.21, 40),
                                                 sensitivity: .standard, now: now))
    }

    func testGatedFramesExcluded() {
        let now = t0.addingTimeInterval(1.0)
        // 40 帧门控残留 0.3（跨度 1.95s）+ 20 帧未门控静音 → 未门控部分是静音，拒。
        // 不排除门控帧的话中位数会落在 0.3 上而误放行。
        var mixed = frames(0.3, 40, gated: true)
        mixed += frames(0.004, 20, from: t0.addingTimeInterval(0.1))
        XCTAssertFalse(NearFieldGate.shouldAccept(frames: mixed, sensitivity: .standard, now: now))
    }

    func testShortUtteranceWithTrailingSilenceAccepted() {
        // 0.4s 说话 + 0.6s 判停静音：直接取中位数会落在静音上，靠噪声本底剔除救回来
        let mixed = frames(0.21, 8) + frames(0.003, 12, from: t0.addingTimeInterval(0.4))
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: mixed, sensitivity: .standard,
                                                 now: t0.addingTimeInterval(1.0)))
    }

    func testFarFieldWithTrailingSilenceRejected() {
        let mixed = frames(0.006, 8) + frames(0.003, 12, from: t0.addingTimeInterval(0.4))
        XCTAssertFalse(NearFieldGate.shouldAccept(frames: mixed, sensitivity: .standard,
                                                  now: t0.addingTimeInterval(1.0)))
    }

    func testAllSilenceRejected() {
        XCTAssertFalse(NearFieldGate.shouldAccept(frames: frames(0.003, 40),
                                                  sensitivity: .standard,
                                                  now: t0.addingTimeInterval(2.0)))
    }

    func testStaleFramesIgnored() {
        let now = t0.addingTimeInterval(5)
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: frames(0.21, 40),
                                                 sensitivity: .standard, now: now))
    }

    func testMixedSpeakers() {
        let now = t0.addingTimeInterval(2.0)
        // 窗内有近场人声就放行（距离闸不区分是谁）
        let withNear = frames(0.004, 30) + frames(0.21, 10, from: t0.addingTimeInterval(1.5))
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: withNear, sensitivity: .standard, now: now))
        // 只有远场帧 → 拒
        let farOnly = frames(0.006, 30) + frames(0.003, 10, from: t0.addingTimeInterval(1.5))
        XCTAssertFalse(NearFieldGate.shouldAccept(frames: farOnly, sensitivity: .standard, now: now))
    }

    func testFarFieldWithSingleSpikeStillRejected() {
        // 碰一下麦克风（单帧 0.55）不构成「有人说话」
        var mixed = frames(0.006, 39)
        mixed.append(.init(at: t0.addingTimeInterval(1.95), level: 0.55, gated: false))
        XCTAssertFalse(NearFieldGate.shouldAccept(frames: mixed, sensitivity: .standard,
                                                  now: t0.addingTimeInterval(2.0)))
    }

    func testLooseAcceptsContinuousLowSpeech() {
        // 宽松档 0.02：连续 0.03 的人声不能被噪声本底剔掉（cap 跟着阈值走）
        XCTAssertTrue(NearFieldGate.shouldAccept(frames: frames(0.03, 40), sensitivity: .loose,
                                                 now: t0.addingTimeInterval(2.0)))
    }

    func testKeepAliveFarFieldRejectedNearFieldAccepted() {
        let now = t0.addingTimeInterval(0.5)
        XCTAssertFalse(NearFieldGate.shouldKeepAlive(frames: frames(0.006, 10),
                                                    sensitivity: .standard, now: now))
        XCTAssertTrue(NearFieldGate.shouldKeepAlive(frames: frames(0.21, 10),
                                                    sensitivity: .standard, now: now))
    }

    func testKeepAliveOffAlwaysTrue() {
        XCTAssertTrue(NearFieldGate.shouldKeepAlive(frames: frames(0.001, 10),
                                                    sensitivity: .off,
                                                    now: t0.addingTimeInterval(0.5)))
    }

    func testKeepAliveGatedFramesIgnored() {
        XCTAssertFalse(NearFieldGate.shouldKeepAlive(frames: frames(0.3, 10, gated: true),
                                                     sensitivity: .standard,
                                                     now: t0.addingTimeInterval(0.5)))
    }

    func testKeepAliveStaleFramesRejected() {
        XCTAssertFalse(NearFieldGate.shouldKeepAlive(frames: frames(0.21, 10),
                                                     sensitivity: .standard,
                                                     now: t0.addingTimeInterval(5)))
    }

    // MARK: 长按说话：整段判定（帧不够 = 太短，不是 fail-open）

    func testHoldSpanOffAlwaysAccepts() {
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: frames(0.001, 40), sensitivity: .off), .accept)
    }

    func testHoldSpanNearFieldAcceptedFarFieldRejected() {
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: frames(0.21, 40), sensitivity: .standard), .accept)
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: frames(0.006, 40), sensitivity: .standard), .reject)
    }

    func testHoldSpanAllSilenceRejected() {
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: frames(0.003, 40), sensitivity: .standard), .reject)
    }

    func testHoldSpanTooShortWhenSpanUnderMinimum() {
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: frames(0.21, 10), sensitivity: .standard), .tooShort)
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: [], sensitivity: .standard), .tooShort)
    }

    func testHoldSpanWithTrailingSilence() {
        let far = frames(0.006, 8) + frames(0.003, 12, from: t0.addingTimeInterval(0.4))
        let near = frames(0.21, 8) + frames(0.003, 12, from: t0.addingTimeInterval(0.4))
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: far, sensitivity: .standard), .reject)
        XCTAssertEqual(NearFieldGate.judgeHoldSpan(frames: near, sensitivity: .standard), .accept)
    }

    func testMedianAndPercentile() {
        XCTAssertEqual(NearFieldGate.median([]), 0)
        XCTAssertEqual(NearFieldGate.median([3, 1, 2]), 2)
        XCTAssertEqual(NearFieldGate.median([4, 1, 3, 2]), 2)
        XCTAssertEqual(NearFieldGate.percentile([1, 2, 3, 4, 5], 0.2), 1)
        XCTAssertEqual(NearFieldGate.percentile([1, 2, 3, 4, 5], 0.9), 4)
    }

    // MARK: 提交闸的证据（任意 2 秒窗有近处人声即记，docs/bugs/058）

    func testContainsNearSpeechNearFieldAcceptedFarFieldRejected() {
        let now = t0.addingTimeInterval(2.0)
        XCTAssertTrue(NearFieldGate.containsNearSpeech(frames: frames(0.21, 40),
                                                       sensitivity: .standard, now: now))
        XCTAssertFalse(NearFieldGate.containsNearSpeech(frames: frames(0.006, 40),
                                                        sensitivity: .standard, now: now))
    }

    func testContainsNearSpeechNeedsEnoughData() {
        // 数据不足不记证据（句子到达时仍走 shouldAccept 的 fail-open）
        XCTAssertFalse(NearFieldGate.containsNearSpeech(frames: frames(0.21, 10),
                                                        sensitivity: .standard,
                                                        now: t0.addingTimeInterval(0.45)))
        XCTAssertFalse(NearFieldGate.containsNearSpeech(frames: [], sensitivity: .standard, now: t0))
    }

    func testContainsNearSpeechOffAlwaysTrue() {
        XCTAssertTrue(NearFieldGate.containsNearSpeech(frames: frames(0.001, 10),
                                                       sensitivity: .off, now: t0))
    }

    func testContainsNearSpeechIgnoresFramesBeforeLastSentence() {
        // 上一句已提交：2 秒窗里还留着它的近场帧，但不能给下一句当证据（058 评审 P1）
        let mixed = frames(0.21, 30) + frames(0.006, 10, from: t0.addingTimeInterval(1.5))
        let now = t0.addingTimeInterval(2.2)
        XCTAssertTrue(NearFieldGate.containsNearSpeech(frames: mixed, sensitivity: .standard, now: now))
        XCTAssertFalse(NearFieldGate.containsNearSpeech(frames: mixed, sensitivity: .standard,
                                                        since: t0.addingTimeInterval(2.0), now: now))
    }

    func testContainsNearSpeechIgnoresGatedFrames() {
        // 播报期的 TTS 残留（门控帧 0.3）不能当证据
        let now = t0.addingTimeInterval(1.0)
        var mixed = frames(0.3, 40, gated: true)
        mixed += frames(0.004, 20, from: t0.addingTimeInterval(0.1))
        XCTAssertFalse(NearFieldGate.containsNearSpeech(frames: mixed,
                                                        sensitivity: .standard, now: now))
    }
}
