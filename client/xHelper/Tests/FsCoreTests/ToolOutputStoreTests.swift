import XCTest
@testable import FsCore

final class ToolOutputStoreTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("ToolOutputStoreTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    func testSaveReusesByContentHash() throws {
        let store = ToolOutputStore(baseURL: dir, retention: 3600, maxTotalBytes: 1024 * 1024)
        let a = try XCTUnwrap(store.save("hello 世界"))
        let b = try XCTUnwrap(store.save("hello 世界"))

        XCTAssertEqual(a, b, "同内容应复用同一文件（内容寻址）")
        XCTAssertTrue(a.lastPathComponent.hasPrefix(ToolOutputStore.fileNamePrefix))
        XCTAssertEqual(try String(contentsOf: a, encoding: .utf8), "hello 世界")

        let files = try FileManager.default.contentsOfDirectory(atPath: dir.path)
        XCTAssertEqual(files.count, 1, "同内容不应新增文件")
    }

    func testSaveRefreshesMtimeOnDedup() throws {
        let store = ToolOutputStore(baseURL: dir, retention: 3600, maxTotalBytes: 1024 * 1024)
        let url = try XCTUnwrap(store.save("same content"))
        let stale = Date().addingTimeInterval(-1000)
        try FileManager.default.setAttributes([.modificationDate: stale], ofItemAtPath: url.path)

        _ = try XCTUnwrap(store.save("same content"), "同内容应复用同一文件")

        let attrs = try FileManager.default.attributesOfItem(atPath: url.path)
        let mtime = try XCTUnwrap(attrs[.modificationDate] as? Date)
        XCTAssertGreaterThan(mtime, stale, "去重命中时应刷新 mtime，让 LRU 反映最近使用")
    }

    func testSaveDifferentContentDifferentFiles() throws {
        let store = ToolOutputStore(baseURL: dir, retention: 3600, maxTotalBytes: 1024 * 1024)
        _ = try XCTUnwrap(store.save("a"))
        _ = try XCTUnwrap(store.save("b"))
        let files = try FileManager.default.contentsOfDirectory(atPath: dir.path)
        XCTAssertEqual(files.count, 2)
    }

    func testCleanupRemovesExpired() throws {
        let store = ToolOutputStore(baseURL: dir, retention: 3600, maxTotalBytes: 1024 * 1024)
        let old = try XCTUnwrap(store.save("old"))
        let fresh = try XCTUnwrap(store.save("fresh"))
        try FileManager.default.setAttributes(
            [.modificationDate: Date().addingTimeInterval(-7200)], ofItemAtPath: old.path)

        store.cleanup()

        XCTAssertFalse(FileManager.default.fileExists(atPath: old.path), "超期文件应被删除")
        XCTAssertTrue(FileManager.default.fileExists(atPath: fresh.path), "未超期文件应保留")
    }

    func testCleanupLRUWhenOverCap() throws {
        // 容量上限 10 字节：两个 10 字节文件 → 删最旧的，保留最新
        let store = ToolOutputStore(baseURL: dir, retention: 100000, maxTotalBytes: 10)
        let a = try XCTUnwrap(store.save("aaaaaaaaaa"))
        let b = try XCTUnwrap(store.save("bbbbbbbbbb"))
        try FileManager.default.setAttributes(
            [.modificationDate: Date().addingTimeInterval(-100)], ofItemAtPath: a.path)
        try FileManager.default.setAttributes(
            [.modificationDate: Date()], ofItemAtPath: b.path)

        store.cleanup()

        XCTAssertFalse(FileManager.default.fileExists(atPath: a.path), "超容量时最旧文件应被 LRU 删除")
        XCTAssertTrue(FileManager.default.fileExists(atPath: b.path))
    }

    func testClearAll() throws {
        let store = ToolOutputStore(baseURL: dir, retention: 3600, maxTotalBytes: 1024)
        _ = try XCTUnwrap(store.save("x"))
        store.clearAll()
        let files = (try? FileManager.default.contentsOfDirectory(atPath: dir.path)) ?? []
        XCTAssertTrue(files.isEmpty)
    }

    func testContentFileNameStableAndDistinct() {
        let one = ToolOutputStore.contentFileName(for: Data("same".utf8))
        let two = ToolOutputStore.contentFileName(for: Data("same".utf8))
        let other = ToolOutputStore.contentFileName(for: Data("other".utf8))
        XCTAssertEqual(one, two)
        XCTAssertNotEqual(one, other)
    }
}
