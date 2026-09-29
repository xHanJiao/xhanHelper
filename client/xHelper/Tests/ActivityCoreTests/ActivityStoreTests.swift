import XCTest
@testable import ActivityCore

final class ActivityStoreTests: XCTestCase {

    private func makeTempDir() throws -> URL {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("activity-test-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }

    private func makeEvent(_ type: String) -> ActivityEvent {
        ActivityEvent(type: type, actor: "user", trigger: "manual",
                      attrs: ["n": .number(1), "s": .string("x"), "b": .bool(true)])
    }

    private func bigEvent(_ type: String, bytes: Int) -> ActivityEvent {
        ActivityEvent(type: type, actor: "system", trigger: "manual",
                      attrs: ["pad": .string(String(repeating: "x", count: bytes))])
    }

    func testAppendSealLoadRoundtrip() async throws {
        let dir = try makeTempDir()
        let store = ActivityStore(directory: dir)
        let a = makeEvent("a")
        let b = makeEvent("b")

        try await store.append([a, b])
        let beforeSeal = try await store.pendingSegments()
        XCTAssertTrue(beforeSeal.isEmpty, "未封段时没有待传段")

        let sealed = try await store.sealCurrent()
        XCTAssertNotNil(sealed)
        let pending = try await store.pendingSegments()
        XCTAssertEqual(pending.count, 1)

        let loaded = try await store.load(pending[0])
        XCTAssertEqual(loaded, [a, b], "JSONL 往返保真（含 attrs）")
        XCTAssertEqual(ActivityStore.segmentID(pending[0]).hasPrefix("seg-"), true)
    }

    func testRotatesAtMaxSegmentEvents() async throws {
        let dir = try makeTempDir()
        let store = ActivityStore(directory: dir)
        let batch = (0..<ActivityStore.maxSegmentEvents).map { makeEvent("e\($0)") }

        try await store.append(batch)
        var pending = try await store.pendingSegments()
        XCTAssertEqual(pending.count, 1, "满 500 条自动封段")

        try await store.append([makeEvent("tail")])
        pending = try await store.pendingSegments()
        XCTAssertEqual(pending.count, 1, "新一段还没封")
        let sealed = try await store.sealCurrent()
        XCTAssertNotNil(sealed)
        pending = try await store.pendingSegments()
        XCTAssertEqual(pending.count, 2)
    }

    func testAppendRepairsTornLine() async throws {
        let dir = try makeTempDir()
        let current = dir.appendingPathComponent("current.jsonl")
        // 模拟上次进程被杀：末行是半截无换行的 JSON
        try Data("{\"event_id\":\"torn\",\"type\":\"x\"".utf8).write(to: current)

        let store = ActivityStore(directory: dir)
        let good = makeEvent("good")
        try await store.append([good])
        let sealed = try await store.sealCurrent()
        let url = try XCTUnwrap(sealed)

        let loaded = try await store.load(url)
        XCTAssertEqual(loaded, [good], "半行被跳过，新事件存活（没被拼成一行一起丢）")
    }

    func testSealsOnByteLimit() async throws {
        let dir = try makeTempDir()
        let store = ActivityStore(directory: dir)
        let chunk = ActivityStore.maxSegmentBytes / 2
        try await store.append([bigEvent("a", bytes: chunk)])
        try await store.append([bigEvent("b", bytes: chunk)])
        let pending = try await store.pendingSegments()
        XCTAssertEqual(pending.count, 1, "字节超 1 MiB 提前封段")
    }

    func testQuarantineHidesSegment() async throws {
        let dir = try makeTempDir()
        let store = ActivityStore(directory: dir)
        try await store.append([makeEvent("a")])
        let sealed = try await store.sealCurrent()
        let url = try XCTUnwrap(sealed)

        await store.quarantine(url)
        let pending = try await store.pendingSegments()
        XCTAssertEqual(pending.count, 0, "隔离后不再被列出")
        XCTAssertTrue(FileManager.default.fileExists(atPath: url.path + ".bad"))
    }

    func testRecorderFillsIdentityAndDeletesOnAck() async throws {
        let dir = try makeTempDir()
        let calls = Calls()
        let rec = ActivityRecorder(directory: dir)
        await rec.configure(transport: { id, evs in
            await calls.add(id, evs)
            return ActivityUploadAck(accepted: evs.count, duplicate: 0, watermark: "w", watermarkKind: "time")
        })

        await rec.record(makeEvent("client.app.launched"))
        await rec.uploadNow()

        let sent = await calls.all()
        XCTAssertEqual(sent.count, 1)
        let ev = try XCTUnwrap(sent.first?.1.first)
        XCTAssertFalse(ev.eventId.isEmpty, "event_id 由 recorder 补")
        XCTAssertFalse(ev.occurredAt.isEmpty, "occurred_at 由 recorder 补")
        XCTAssertFalse(ev.clientInstanceId?.isEmpty ?? true)
        XCTAssertEqual(ev.clientBuild, ActivityIdentity.clientBuild)
        XCTAssertEqual(ev.osVersion, ActivityIdentity.osVersion)
        XCTAssertTrue(sent.first?.0.hasPrefix("seg-") ?? false)

        let remaining = try await ActivityStore(directory: dir).pendingSegments()
        XCTAssertEqual(remaining.count, 0, "ack 后删段")
    }

    func testRecorderBatchPreservesOrderAndLaunchID() async throws {
        let dir = try makeTempDir()
        let calls = Calls()
        let rec = ActivityRecorder(directory: dir)
        await rec.configure(transport: { id, evs in
            await calls.add(id, evs)
            return ActivityUploadAck(accepted: evs.count, duplicate: 0, watermark: "w", watermarkKind: "time")
        })

        await rec.record([makeEvent("system.sleep"), makeEvent("system.boundary")])
        await rec.uploadNow()

        let sent = await calls.all()
        let evs = try XCTUnwrap(sent.first?.1)
        XCTAssertEqual(evs.map(\.type), ["system.sleep", "system.boundary"], "同一批按序落盘")
        XCTAssertEqual(evs[0].launchId, ActivityIdentity.launchID, "recorder 盖 launch_id")
        XCTAssertEqual(evs[0].launchId, evs[1].launchId)
        XCTAssertLessThanOrEqual(evs[0].occurredAt, evs[1].occurredAt, "boundary 不早于它要截断的事件")
    }

    func testRecorderKeepsSegmentOnRetryable() async throws {
        let dir = try makeTempDir()
        let rec = ActivityRecorder(directory: dir)
        await rec.configure(transport: { _, _ in throw ActivityUploadError.retryable(URLError(.notConnectedToInternet)) })

        await rec.record(makeEvent("a"))
        await rec.uploadNow()

        let pending = try await ActivityStore(directory: dir).pendingSegments()
        XCTAssertEqual(pending.count, 1, "可重试错误留着下次")
    }

    func testRecorderQuarantinesOnClientError() async throws {
        let dir = try makeTempDir()
        let rec = ActivityRecorder(directory: dir)
        await rec.configure(transport: { _, _ in throw ActivityUploadError.client(nil) })

        await rec.record(makeEvent("a"))
        await rec.uploadNow()

        let pending = try await ActivityStore(directory: dir).pendingSegments()
        XCTAssertEqual(pending.count, 0, "4xx 段被隔离，不阻塞后续")
    }
}

private actor Calls {
    private var segments: [(String, [ActivityEvent])] = []
    func add(_ id: String, _ events: [ActivityEvent]) { segments.append((id, events)) }
    func all() -> [(String, [ActivityEvent])] { segments }
}
