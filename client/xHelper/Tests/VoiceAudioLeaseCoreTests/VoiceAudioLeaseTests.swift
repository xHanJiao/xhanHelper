import XCTest
@testable import VoiceAudioLeaseCore

final class VoiceAudioLeaseTests: XCTestCase {
    private final class Owner {}

    func testCaptureIsExclusiveAndIdempotentForSameOwner() {
        let a = Owner(), b = Owner()
        var lease = VoiceAudioLease()

        XCTAssertEqual(lease.beginCapture(ObjectIdentifier(a)), .started)
        // 同一个 owner 重复开：幂等，不抛错也不改状态
        XCTAssertEqual(lease.beginCapture(ObjectIdentifier(a)), .alreadyMine)
        // 别人来开：后到者被拒
        XCTAssertEqual(lease.beginCapture(ObjectIdentifier(b)), .busy)
        XCTAssertEqual(lease.captureOwner, ObjectIdentifier(a))
    }

    func testNonOwnerCannotStopCapture() {
        let a = Owner(), b = Owner()
        var lease = VoiceAudioLease()
        _ = lease.beginCapture(ObjectIdentifier(a))

        XCTAssertFalse(lease.endCapture(ObjectIdentifier(b)))
        XCTAssertEqual(lease.captureOwner, ObjectIdentifier(a))

        XCTAssertTrue(lease.endCapture(ObjectIdentifier(a)))
        XCTAssertNil(lease.captureOwner)
    }

    func testPlaybackPreemption() {
        let a = Owner(), b = Owner()
        var lease = VoiceAudioLease()

        XCTAssertNil(lease.beginPlayback(ObjectIdentifier(a)))
        // 同一 owner 重播不算抢占
        XCTAssertNil(lease.beginPlayback(ObjectIdentifier(a)))
        // 新 owner 抢占：返回被抢的那个
        XCTAssertEqual(lease.beginPlayback(ObjectIdentifier(b)), ObjectIdentifier(a))
        XCTAssertEqual(lease.playbackOwner, ObjectIdentifier(b))
    }

    func testNonOwnerCannotStopPlayback() {
        let a = Owner(), b = Owner()
        var lease = VoiceAudioLease()
        _ = lease.beginPlayback(ObjectIdentifier(a))

        XCTAssertFalse(lease.endPlayback(ObjectIdentifier(b)))
        XCTAssertEqual(lease.playbackOwner, ObjectIdentifier(a))
        XCTAssertTrue(lease.endPlayback(ObjectIdentifier(a)))
    }

    func testVoiceBarPinIsPerOwner() {
        let a = Owner(), b = Owner()
        var lease = VoiceAudioLease()

        lease.setVoiceBar(ObjectIdentifier(a), open: true)
        lease.setVoiceBar(ObjectIdentifier(b), open: true)
        XCTAssertTrue(lease.voiceBarOpen)

        // ptt 退出只清自己的 pin，不能把视图那边的清掉
        lease.setVoiceBar(ObjectIdentifier(a), open: false)
        XCTAssertTrue(lease.voiceBarOpen)

        lease.setVoiceBar(ObjectIdentifier(b), open: false)
        XCTAssertFalse(lease.voiceBarOpen)
    }

    func testReleaseOnlyWhenNothingInUse() {
        let a = Owner(), b = Owner()
        var lease = VoiceAudioLease()
        XCTAssertTrue(lease.canReleaseDevice)

        _ = lease.beginCapture(ObjectIdentifier(a))
        XCTAssertFalse(lease.canReleaseDevice)
        _ = lease.endCapture(ObjectIdentifier(a))
        XCTAssertTrue(lease.canReleaseDevice)

        _ = lease.beginPlayback(ObjectIdentifier(b))
        XCTAssertFalse(lease.canReleaseDevice)
        _ = lease.endPlayback(ObjectIdentifier(b))
        XCTAssertTrue(lease.canReleaseDevice)

        lease.setVoiceBar(ObjectIdentifier(a), open: true)
        XCTAssertFalse(lease.canReleaseDevice)
        lease.setVoiceBar(ObjectIdentifier(a), open: false)
        XCTAssertTrue(lease.canReleaseDevice)
    }
}
