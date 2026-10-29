import XCTest
@testable import FsCore

/// 日历目标挑选 + 事件字段（CalendarEventCore）的单测：改动挑选规则先让这里给出新预期。
final class CalendarCoreTests: XCTestCase {

    private func cand(_ id: String, _ title: String, _ account: String, _ source: String,
                      modifiable: Bool = true, isDefault: Bool = false) -> CalendarCore.Candidate {
        .init(id: id, title: title, accountTitle: account, sourceType: source,
              allowsModification: modifiable, isDefault: isDefault)
    }

    // MARK: - 可同步判定

    func testLocalAndSubscribedAreNotSyncable() {
        XCTAssertFalse(CalendarCore.isSyncable(cand("1", "我的日历", "On My Mac", "local")))
        XCTAssertFalse(CalendarCore.isSyncable(cand("2", "节假日", "其他", "subscribed")))
        XCTAssertFalse(CalendarCore.isSyncable(cand("3", "生日", "其他", "birthdays")))
        XCTAssertFalse(CalendarCore.isSyncable(cand("4", "工作", "iCloud", "calDAV", modifiable: false)))
        XCTAssertTrue(CalendarCore.isSyncable(cand("5", "工作", "iCloud", "calDAV")))
        XCTAssertTrue(CalendarCore.isSyncable(cand("6", "工作", "Google", "calDAV")))
    }

    // MARK: - 挑选顺序

    func testPrefersDefaultWhenSyncable() {
        let list = [cand("1", "工作", "iCloud", "calDAV"),
                    cand("2", "个人", "iCloud", "calDAV", isDefault: true)]
        XCTAssertEqual(CalendarCore.pick(from: list), .ok(id: "2"))
    }

    func testSkipsLocalDefaultForICloud() {
        let list = [cand("1", "本地", "On My Mac", "local", isDefault: true),
                    cand("2", "工作", "iCloud", "calDAV")]
        XCTAssertEqual(CalendarCore.pick(from: list), .ok(id: "2"))
    }

    func testFallsBackToAnySyncable() {
        let list = [cand("1", "本地", "On My Mac", "local", isDefault: true),
                    cand("2", "家庭", "Google", "calDAV")]
        XCTAssertEqual(CalendarCore.pick(from: list), .ok(id: "2"))
    }

    func testNoSyncableReportsAvailable() {
        let list = [cand("1", "本地", "On My Mac", "local", isDefault: true)]
        XCTAssertEqual(CalendarCore.pick(from: list), .noSyncable(available: ["本地（On My Mac）"]))
    }

    // MARK: - 指定名字

    func testExplicitExactThenContains() {
        let list = [cand("1", "工作", "iCloud", "calDAV"),
                    cand("2", "家庭共享", "iCloud", "calDAV")]
        XCTAssertEqual(CalendarCore.pick(from: list, explicitName: "家庭共享"), .ok(id: "2"))
        XCTAssertEqual(CalendarCore.pick(from: list, explicitName: "家庭"), .ok(id: "2"))
    }

    func testExplicitLocalCalendarIsRejected() {
        let list = [cand("1", "工作", "iCloud", "calDAV"),
                    cand("2", "本地", "On My Mac", "local")]
        XCTAssertEqual(CalendarCore.pick(from: list, explicitName: "本地"),
                       .explicitUnavailable(available: ["工作（iCloud）"]))
    }

    func testExplicitMissListsSyncableOnly() {
        let list = [cand("1", "工作", "iCloud", "calDAV"),
                    cand("2", "本地", "On My Mac", "local")]
        XCTAssertEqual(CalendarCore.pick(from: list, explicitName: "不存在"),
                       .explicitUnavailable(available: ["工作（iCloud）"]))
    }

    // MARK: - 事件字段

    func testEventTitleFlattensAndTruncates() {
        XCTAssertEqual(CalendarCore.eventTitle("  倒垃圾 \n"), "倒垃圾")
        XCTAssertEqual(CalendarCore.eventTitle(String(repeating: "字", count: 70), limit: 60),
                       String(repeating: "字", count: 60) + "…")
        XCTAssertEqual(CalendarCore.eventTitle("多行\r\n标题"), "多行 标题")
    }

    func testIsMirrorable() {
        let future = Date().addingTimeInterval(600)
        XCTAssertTrue(CalendarCore.isMirrorable(status: "pending", body: "八点倒垃圾", deadline: future, now: Date()))
        XCTAssertFalse(CalendarCore.isMirrorable(status: "generating", body: "有正文", deadline: future, now: Date()))
        XCTAssertFalse(CalendarCore.isMirrorable(status: "pending", body: "", deadline: future, now: Date()))
        XCTAssertFalse(CalendarCore.isMirrorable(status: "pending", body: "有正文", deadline: Date().addingTimeInterval(-60), now: Date()))
    }
}
