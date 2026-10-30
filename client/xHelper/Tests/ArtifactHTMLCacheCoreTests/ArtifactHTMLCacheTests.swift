import XCTest
@testable import ArtifactHTMLCacheCore

final class ArtifactHTMLCacheTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("ArtifactHTMLCacheTests-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        if let dir { try? FileManager.default.removeItem(at: dir) }
    }

    func testStoreLoadRemove() throws {
        let cache = ArtifactHTMLCache(dir: dir)
        let etag = "\"v1\""

        XCTAssertNil(cache.load(id: "a1"))
        try cache.store(id: "a1", html: "<html>一</html>", etag: etag)
        XCTAssertEqual(cache.load(id: "a1")?.html, "<html>一</html>")
        XCTAssertEqual(cache.load(id: "a1")?.etag, etag)

        cache.remove(ids: ["a1"])
        XCTAssertNil(cache.load(id: "a1"))
        cache.remove(ids: ["a1"]) // 幂等
    }

    func testStoreWithoutEtagClearsOldTag() throws {
        let cache = ArtifactHTMLCache(dir: dir)
        try cache.store(id: "a1", html: "旧", etag: "\"v1\"")
        try cache.store(id: "a1", html: "新", etag: nil)

        XCTAssertEqual(cache.load(id: "a1")?.html, "新")
        XCTAssertNil(cache.load(id: "a1")?.etag)
    }

    func testPruneDeletesOldest() throws {
        // 每条 html 8 字节 + etag 3 字节；上限 15 → 存第二条后必须淘汰最旧那条
        let cache = ArtifactHTMLCache(dir: dir, maxBytes: 15)
        try cache.store(id: "old", html: "12345678", etag: "\"a\"")
        setModificationDate(dir.appendingPathComponent("old.html"), .init(timeIntervalSince1970: 1))
        setModificationDate(dir.appendingPathComponent("old.etag"), .init(timeIntervalSince1970: 1))

        try cache.store(id: "new", html: "12345678", etag: "\"b\"")

        XCTAssertNil(cache.load(id: "old"))
        XCTAssertEqual(cache.load(id: "new")?.html, "12345678")
    }

    func testTouchBumpsMtime() throws {
        let cache = ArtifactHTMLCache(dir: dir)
        try cache.store(id: "a1", html: "x", etag: "\"v1\"")
        let url = dir.appendingPathComponent("a1.html")
        setModificationDate(url, .init(timeIntervalSince1970: 1))

        cache.touch(id: "a1")

        let mtime = try FileManager.default.attributesOfItem(atPath: url.path)[.modificationDate] as? Date
        XCTAssertGreaterThan(mtime ?? .distantPast, Date(timeIntervalSince1970: 1))
    }

    func testRejectsUnsafeID() throws {
        let cache = ArtifactHTMLCache(dir: dir)

        XCTAssertNil(cache.load(id: "../evil"))
        XCTAssertThrowsError(try cache.store(id: "../evil", html: "x", etag: nil))
        cache.remove(ids: ["../evil"]) // 不崩即可
    }

    private func setModificationDate(_ url: URL, _ date: Date) {
        try? FileManager.default.setAttributes([.modificationDate: date], ofItemAtPath: url.path)
    }
}
