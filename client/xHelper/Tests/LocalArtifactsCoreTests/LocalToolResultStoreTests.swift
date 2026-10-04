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

    private let from = "2026-09-19T00:00:00Z"
    private let to = "2026-09-19T23:59:59Z"

    func testUpsertThenEntriesRoundTrip() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")

        let entries = try await store.entries(for: "c1", from: from, to: to)
        XCTAssertEqual(entries.count, 1)
        XCTAssertEqual(entries.first?.id, "m1")
        XCTAssertEqual(entries.first?.type, .chart)
        XCTAssertEqual(entries.first?.payload, data("A"))
        XCTAssertEqual(entries.first?.createdAt, "2026-09-19T10:00:00Z")
    }

    func testConversationsAreIsolated() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")
        try await store.upsert(type: .docRefs, payload: data("B"), id: "m2", createdAt: "2026-09-19T11:00:00Z", conversationId: "c2")

        let c1 = try await store.entries(for: "c1", from: from, to: to)
        let c2 = try await store.entries(for: "c2", from: from, to: to)
        XCTAssertEqual(c1.map(\.id), ["m1"])
        XCTAssertEqual(c2.map(\.id), ["m2"])
    }

    func testUpsertSameIdReplacesWithoutDuplicate() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .docRefs, payload: data("old"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")
        try await store.upsert(type: .docRefs, payload: data("new"), id: "m1", createdAt: "2026-09-19T10:05:00Z", conversationId: "c1")

        let entries = try await store.entries(for: "c1", from: from, to: to)
        XCTAssertEqual(entries.count, 1, "同 id 应覆盖不重复")
        XCTAssertEqual(entries.first?.payload, data("new"))
        XCTAssertEqual(entries.first?.createdAt, "2026-09-19T10:05:00Z")
    }

    func testRangeFilterIsInclusive() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("edge-low"), id: "lo", createdAt: from, conversationId: "c1")
        try await store.upsert(type: .chart, payload: data("edge-high"), id: "hi", createdAt: to, conversationId: "c1")
        try await store.upsert(type: .chart, payload: data("outside"), id: "out", createdAt: "2026-09-20T00:00:00Z", conversationId: "c1")

        let entries = try await store.entries(for: "c1", from: from, to: to)
        XCTAssertEqual(entries.map(\.id), ["lo", "hi"], "边界含两端，范围外剔除")
    }

    func testPersistsAcrossInstances() async throws {
        let writer = LocalToolResultStore(baseURL: dir)
        try await writer.upsert(type: .docRefs, payload: data("D"), id: "m9", createdAt: "2026-09-19T10:00:00Z", conversationId: "cX")

        let reader = LocalToolResultStore(baseURL: dir)
        let entries = try await reader.entries(for: "cX", from: from, to: to)
        XCTAssertEqual(entries.map(\.id), ["m9"], "新实例应能从磁盘读回")
        XCTAssertEqual(entries.first?.payload, data("D"))
    }

    func testClearAllWipesDiskAndCache() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "c1")
        try await store.clearAll()

        let afterClear = try await store.entries(for: "c1", from: from, to: to)
        XCTAssertEqual(afterClear.count, 0)
        let reader = LocalToolResultStore(baseURL: dir)
        let reloaded = try await reader.entries(for: "c1", from: from, to: to)
        XCTAssertEqual(reloaded.count, 0, "磁盘也应清空")
    }

    /// 回归：本地卡片是 UTC `Z`，服务端窗口是东八区 `+08:00`（本地 SQLite 后端）。
    /// 旧实现按字符串比较，`04:…Z` < `12:…+08:00`，卡片被误剔（刷新即消失）。
    func testRangeFilterHandlesTimezoneOffsetMismatch() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        // 12:04:32+08 == 04:04:32Z，落在 [12:00,13:00)+08:00 内
        try await store.upsert(type: .docRefs, payload: data("in"), id: "in",
                               createdAt: "2026-09-26T04:04:32Z", conversationId: "c1")
        // 08:00Z == 16:00+08，超出窗口
        try await store.upsert(type: .docRefs, payload: data("out"), id: "out",
                               createdAt: "2026-09-26T08:00:00Z", conversationId: "c1")

        let entries = try await store.entries(
            for: "c1",
            from: "2026-09-26T12:00:00+08:00",
            to: "2026-09-26T13:00:00+08:00"
        )
        XCTAssertEqual(entries.map(\.id), ["in"], "同一时刻的不同偏移必须按时间比，不能按字符串")
    }

    func testRemoveConversationWipesDiskAndCache() async throws {
        let store = LocalToolResultStore(baseURL: dir)
        try await store.upsert(type: .chart, payload: data("A"), id: "m1", createdAt: "2026-09-19T10:00:00Z", conversationId: "")
        try await store.removeConversation("")

        let after = try await store.entries(for: "", from: from, to: to)
        XCTAssertEqual(after.count, 0)
        let reader = LocalToolResultStore(baseURL: dir)
        let reloaded = try await reader.entries(for: "", from: from, to: to)
        XCTAssertEqual(reloaded.count, 0, "磁盘也应清空")
    }
}
