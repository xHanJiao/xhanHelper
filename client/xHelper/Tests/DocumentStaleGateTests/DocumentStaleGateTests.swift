import XCTest
@testable import DocumentStaleGate

/// 「自动拉新正文」的闸门。钉三件事：
/// 同一份屏幕正文上只拉一次（防死循环）、正文换了要重新开口（否则真信号被静默吞掉）、
/// 提示整篇只给一次（别每轮正文变化都吵）。
final class DocumentStaleGateTests: XCTestCase {

    func testReloadsOncePerScreen() {
        var gate = DocumentStaleGate()
        XCTAssertTrue(gate.noteStale(), "第一次发现不一致该拉一次新正文")
        XCTAssertFalse(gate.noteStale(), "同一份屏幕正文上不该拉第二次（拉了也没用）")
        XCTAssertFalse(gate.noteStale())
    }

    func testScreenChangeReopensTheGate() {
        var gate = DocumentStaleGate()
        XCTAssertTrue(gate.noteStale())
        XCTAssertFalse(gate.noteStale())
        gate.screenChanged()
        XCTAssertTrue(gate.noteStale(), "正文换了之后的新不一致是真信号，不该被吞掉")
        gate.screenChanged()
        gate.screenChanged()
        XCTAssertTrue(gate.noteStale(), "连着换两次也还是「新的一屏」，仍该拉")
    }

    func testWarnsOnlyOncePerDocument() {
        var gate = DocumentStaleGate()
        XCTAssertTrue(gate.noteStillUndrawn(), "拉过还画不出该提示一次")
        XCTAssertFalse(gate.noteStillUndrawn(), "同一篇里别重复提示")
        gate.screenChanged()
        XCTAssertFalse(gate.noteStillUndrawn(), "提示整篇只给一次，不随屏幕变化重置")
    }

    /// 刷新与提示互不干扰：拉过之后（同一屏）仍可提示一次。
    func testReloadAndWarnAreIndependent() {
        var gate = DocumentStaleGate()
        XCTAssertTrue(gate.noteStale())
        XCTAssertFalse(gate.noteStale())
        XCTAssertTrue(gate.noteStillUndrawn())
        XCTAssertFalse(gate.noteStillUndrawn())
    }
}
