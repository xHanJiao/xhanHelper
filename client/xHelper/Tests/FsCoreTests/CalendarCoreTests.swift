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

    func testMatchIndexAndNotificationDecision() {
        let titles = ["工作", "家庭共享", "节假日"]
        XCTAssertEqual(CalendarCore.matchIndex(explicitName: "工作", in: titles), 0)
        XCTAssertEqual(CalendarCore.matchIndex(explicitName: "家庭", in: titles), 1) // 包含匹配
        XCTAssertNil(CalendarCore.matchIndex(explicitName: "不存在", in: titles))
        XCTAssertNil(CalendarCore.matchIndex(explicitName: "  ", in: titles))

        // 已镜像的不再排通知，其余照旧（用户手删后回落通知也走这条）
        XCTAssertEqual(
            CalendarCore.needingNotification(pendingIDs: ["a", "b", "c"], covered: ["b"]),
            ["a", "c"])
        XCTAssertEqual(CalendarCore.needingNotification(pendingIDs: ["a"], covered: []), ["a"])
        XCTAssertEqual(CalendarCore.needingNotification(pendingIDs: [], covered: ["a"]), [])
    }

    // MARK: - 通知排布 / 确认条摘要（补测）

    func testNotificationPlan() {
        let local: Set<String> = ["a", "b", "z"]
        // 关开关 / 已镜像：不排、并把残留通知撤掉；已经排过的（b）不重复排
        let p1 = CalendarCore.notificationPlan(localIDs: local, pendingIDs: ["a", "b", "c"],
                                               eligible: ["a", "b", "c"], mirrored: ["a"], truncated: false)
        XCTAssertEqual(p1.toSchedule, ["c"])
        XCTAssertTrue(p1.toRemove.contains("a"))

        // 截断（还有下一页）：不许按「不在本页」撤
        let p2 = CalendarCore.notificationPlan(localIDs: local, pendingIDs: ["a"], eligible: ["a"],
                                               mirrored: [], truncated: true)
        XCTAssertEqual(p2.toRemove, [])
        let p3 = CalendarCore.notificationPlan(localIDs: local, pendingIDs: ["a"], eligible: ["a"],
                                               mirrored: [], truncated: false)
        XCTAssertEqual(p3.toRemove, ["b", "z"]) // a 还在 pending，保留
    }

    func testConfirmationSummary() {
        // 时区无关：断言用 `CalendarCore.displayDate` 现算，避免 TZ=UTC 下 14:00 变 06:00
        let start = Date(timeIntervalSince1970: 1_800_000_000)
        let end = start.addingTimeInterval(3600)
        let iso = ISO8601DateFormatter()
        let create = CalendarCore.confirmationSummary(function: "create_event", arguments: [
            "title": "评审会",
            "start": iso.string(from: start),
            "end": iso.string(from: end),
            "calendar": "工作",
        ])
        XCTAssertEqual(create,
                       "创建日程：评审会 ｜ \(CalendarCore.displayDate(start)) 至 \(CalendarCore.displayDate(end).suffix(5)) ｜ 工作")

        let noCalendar = CalendarCore.confirmationSummary(function: "create_event", arguments: [
            "title": "倒垃圾", "start": iso.string(from: start),
        ])
        XCTAssertTrue(noCalendar.contains("默认可同步日历"))

        // 改：要能看到「改成几点」，而不只是字段名
        let update = CalendarCore.confirmationSummary(function: "update_event", arguments: [
            "event_id": "E1", "start": iso.string(from: start), "title": "评审会（改）",
        ])
        XCTAssertTrue(update.contains("开始 → \(CalendarCore.displayDate(start))"))
        XCTAssertTrue(update.contains("标题 → 评审会（改）"))

        let del = CalendarCore.confirmationSummary(function: "delete_event", arguments: [
            "event_id": "E1", "occurrence": Int(start.timeIntervalSince1970),
        ])
        XCTAssertTrue(del.hasPrefix("删除日程：E1"))
        XCTAssertTrue(del.contains(CalendarCore.displayDate(start)))

        // 字段顺序无关（排序后拼接）
        let a = CalendarCore.confirmationSummary(function: "x", arguments: ["b": 1, "a": 2])
        let b = CalendarCore.confirmationSummary(function: "x", arguments: ["a": 2, "b": 1])
        XCTAssertEqual(a, b)
    }

    // MARK: - 事件字段

    func testAnchorAndOccurrenceIndex() {
        let start = Date(timeIntervalSince1970: 1_800_000_000)
        XCTAssertEqual(CalendarCore.anchor(start), 1_800_000_000)

        let candidates: [(identifier: String?, start: Int)] = [
            ("A", 1_800_000_000),
            ("A", 1_800_604_800),   // 同一系列的下一次：id 相同
            ("B", 1_800_604_800),
        ]
        // 重复事件必须靠 id + 锚点一起区分
        XCTAssertEqual(CalendarCore.occurrenceIndex(identifier: "A", anchor: 1_800_604_800, candidates: candidates), 1)
        XCTAssertEqual(CalendarCore.occurrenceIndex(identifier: "B", anchor: 1_800_604_800, candidates: candidates), 2)
        // 容差 1 秒：EventKit 往返可能丢亚秒
        XCTAssertEqual(CalendarCore.occurrenceIndex(identifier: "A", anchor: 1_800_000_001, candidates: candidates), 0)
        XCTAssertNil(CalendarCore.occurrenceIndex(identifier: "A", anchor: 1_800_000_500, candidates: candidates))
        XCTAssertNil(CalendarCore.occurrenceIndex(identifier: "C", anchor: 1_800_000_000, candidates: candidates))
    }

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
