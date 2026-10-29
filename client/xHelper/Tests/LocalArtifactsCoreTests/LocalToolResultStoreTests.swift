import XCTest
@testable import LocalArtifactsCore

final class LocalToolResultStoreTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("LocalToolResultStoreTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    private func data(_ s: String) -> Data { Data(s.utf8) }

    func testUpsertThenEntriesRoundTrip() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")

        let entries = try await store.allEntries(for: "c1")
        XCTAssertEqual(entries.count, 1)
        XCTAssertEqual(entries.first?.id, "m1")
        XCTAssertEqual(entries.first?.type, .chart)
        XCTAssertEqual(entries.first?.payload, data("A"))
        XCTAssertEqual(entries.first?.createdAt, "2026-09-19T10:00:00Z")
        XCTAssertNil(entries.first?.anchorUserID)
    }

    func testAnchorRoundTrips() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .docRefs, payload: data("A"), id: "m1",
                               createdAt: "2026-09-19T10:00:00Z", conversationId: "c1", anchorUserID: "u1")

        let entries = try await store.allEntries(for: "c1")
        XCTAssertEqual(entries.first?.anchorUserID, "u1")
    }

    /// 回归（docs/bugs/040）：卡片时间来自客户端时钟、比服务端快几十秒。
    /// 读取**不带上界过滤**，所以「比页尾消息还晚」的卡片必须原样读回来。
    func testEntriesAreNotFilteredByServerClockUpperBound() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .docRefs, payload: data("ahead"), id: "m1",
                               createdAt: "2026-10-28T11:59:33Z", conversationId: "c1")

        let entries = try await store.allEntries(for: "c1")
        XCTAssertEqual(entries.map(\.id), ["m1"], "上界过滤会把这种卡片误剔（刷新即消失）")
    }

    /// 旧版本落盘的 JSON 没有 `anchorUserID` 键，必须能解码成 nil（不做数据迁移）。
    func testLegacyEntryWithoutAnchorStillDecodes() async throws {
        let legacy = """
        [{"id":"m1","createdAt":"2026-09-19T10:00:00Z","type":"chart","payload":"\(data("A").base64EncodedString())"}]
        """
        try Data(legacy.utf8).write(to: dir.appendingPathComponent("c1.json"))

        let store = LocalToolResultStore(baseURL: dir)
        let entries = try await store.allEntries(for: "c1")
        XCTAssertEqual(entries.map(\.id), ["m1"])
        XCTAssertNil(entries.first?.anchorUserID)
    }

    func testReattachAnchorRewritesOnlyMatchingEntries() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .docRefs, payload: data("A"), id: "m1",
                               createdAt: "2026-09-19T10:00:00Z", conversationId: "c1", anchorUserID: "local-old")
        try await store.upsert(type: .docRefs, payload: data("B"), id: "m2",
                               createdAt: "2026-09-19T11:00:00Z", conversationId: "c1", anchorUserID: "u-other")

        try await store.reattachAnchor(conversationId: "c1", from: "local-old", to: "u-server")

        let entries = try await store.allEntries(for: "c1")
        XCTAssertEqual(entries.first(where: { $0.id == "m1" })?.anchorUserID, "u-server")
        XCTAssertEqual(entries.first(where: { $0.id == "m2" })?.anchorUserID, "u-other")
    }

    func testConversationsAreIsolated() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")
        try await store.upsert(type: .docRefs, payload: data("B"), id: "m2", createdAt: "2026-09-19T11:00:00Z", conversationId: "c2")

        let c1 = try await store.allEntries(for: "c1")
        let c2 = try await store.allEntries(for: "c2")
        XCTAssertEqual(c1.map(\.id), ["m1"])
        XCTAssertEqual(c2.map(\.id), ["m2"])
    }

    func testUpsertSameIdReplacesWithoutDuplicate() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .docRefs, payload: data("old"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")
        try await store.upsert(type: .docRefs, payload: data("new"), id: "m1", createdAt: "2026-09-19T10:05:00Z", conversationId: "c1")

        let entries = try await store.allEntries(for: "c1")
        XCTAssertEqual(entries.count, 1, "同 id 应覆盖不重复")
        XCTAssertEqual(entries.first?.payload, data("new"))
        XCTAssertEqual(entries.first?.createdAt, "2026-09-19T10:05:00Z")
    }

    func testPersistsAcrossInstances() async throws {
        let writer = LocalToolResultStore(baseURL: dir)
        try await writer.upsert(type: .docRefs, payload: data("D"), id: "m9", createdAt: "2026-09-19T10:00:00Z", conversationId: "cX")

        let reader = LocalToolResultStore(baseURL: dir)
        let entries = try await reader.allEntries(for: "cX")
        XCTAssertEqual(entries.map(\.id), ["m9"], "新实例应能从磁盘读回")
        XCTAssertEqual(entries.first?.payload, data("D"))
    }

    func testClearAllWipesDiskAndCache() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")
        try await store.clearAll()

        let afterClear = try await store.allEntries(for: "c1")
        XCTAssertEqual(afterClear.count, 0)
        let reader = LocalToolResultStore(baseURL: dir)
        let reloaded = try await reader.allEntries(for: "c1")
        XCTAssertEqual(reloaded.count, 0, "磁盘也应清空")
    }

    func testRemoveConversationWipesDiskAndCache() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "")
        try await store.removeConversation("")

        let after = try await store.allEntries(for: "")
        XCTAssertEqual(after.count, 0)
        let reader = LocalToolResultStore(baseURL: dir)
        let reloaded = try await reader.allEntries(for: "")
        XCTAssertEqual(reloaded.count, 0, "磁盘也应清空")
    }
}

final class LocalArtifactTimeTests: XCTestCase {
    /// 回归（028）：卡片是 UTC `Z`、服务端下界是东八区 `+08:00`，同一时刻不能按字符串比。
    func testLowerBoundHandlesTimezoneOffsetMismatch() {
        XCTAssertTrue(LocalArtifactTime.isOnOrAfter("2026-09-26T04:04:32Z", from: "2026-09-26T12:00:00+08:00"))
        XCTAssertFalse(LocalArtifactTime.isOnOrAfter("2026-09-25T20:00:00Z", from: "2026-09-26T12:00:00+08:00"))
    }

    /// 回归（040）：上界不参与判定，卡片比页尾更晚也不算「越界」。
    func testNoUpperBound() {
        XCTAssertTrue(LocalArtifactTime.isOnOrAfter("2026-10-28T11:59:33Z", from: "2026-10-28T11:00:00Z"))
    }
}
