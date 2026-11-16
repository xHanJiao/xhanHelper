import XCTest
@testable import EpubCore

final class BookStoreTests: XCTestCase {

    private var store: BookStore!
    private var source: URL!

    override func setUpWithError() throws {
        store = BookStore(root: EpubFixture.tempDir())
        source = try SampleBook.epub2()
        addTeardownBlock {
            try? FileManager.default.removeItem(at: self.store.root)
            try? FileManager.default.removeItem(at: self.source.deletingLastPathComponent())
        }
    }

    func testImportWritesExpectedLayout() throws {
        let meta = try store.importBook(source: source)

        XCTAssertEqual(meta.title, "旧格式 & 目录")
        XCTAssertEqual(meta.chapterCount, 2)
        XCTAssertEqual(meta.sourceBytes, (try FileManager.default.attributesOfItem(atPath: source.path))[.size] as? Int)
        XCTAssertNotNil(meta.sourceSHA256)
        XCTAssertEqual(meta.coverFile, "cover.jpg")

        let dir = store.bookDir(meta.id)
        for name in ["meta.json", "source.epub", "cover.jpg"] {
            XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent(name).path), "缺 \(name)")
        }
        XCTAssertTrue(FileManager.default.fileExists(
            atPath: store.contentDir(meta.id).appendingPathComponent("OEBPS/Text/ch1.xhtml").path
        ))
    }

    func testImportTwiceIsRejectedAsDuplicate() throws {
        let first = try store.importBook(source: source)
        XCTAssertThrowsError(try store.importBook(source: source)) { thrown in
            guard let error = thrown as? BookImportError else {
                return XCTFail("应抛 BookImportError，实际 \(thrown)")
            }
            XCTAssertEqual(error, .duplicate(title: first.title))
        }
        XCTAssertEqual(try store.listBooks().books.count, 1)
    }

    func testImportRejectsNonEpubExtension() throws {
        let renamed = source.deletingPathExtension().appendingPathExtension("txt")
        try FileManager.default.moveItem(at: source, to: renamed)
        source = renamed
        XCTAssertThrowsError(try store.importBook(source: renamed)) { thrown in
            guard let error = thrown as? BookImportError, case .invalidFile = error else {
                return XCTFail("应报 invalidFile，实际 \(thrown)")
            }
        }
    }

    func testListSortsByLastReadThenImported() throws {
        let second = try store.importBook(source: source)
        // 第二本换一份内容，绕开查重
        let other = try SampleBook.epub3()
        addTeardownBlock { try? FileManager.default.removeItem(at: other.deletingLastPathComponent()) }
        let first = try store.importBook(source: other)

        var older = first
        older.lastReadAt = Date().addingTimeInterval(-3600)
        try store.save(older)

        let listing = try store.listBooks()
        XCTAssertEqual(listing.books.map(\.id), [second.id, first.id], "最近在读优先，其次导入时间倒序")
        XCTAssertTrue(listing.failures.isEmpty)
    }

    func testListReportsCorruptMetaInsteadOfHidingIt() throws {
        let meta = try store.importBook(source: source)
        try Data("{}".utf8).write(to: store.bookDir(meta.id).appendingPathComponent("meta.json"))

        let listing = try store.listBooks()
        XCTAssertTrue(listing.books.isEmpty)
        XCTAssertEqual(listing.failures.map(\.id), [meta.id], "损坏的 meta 要报出来，不能静默丢掉")
    }

    func testMetaReturnsNilForMissingBook() throws {
        XCTAssertNil(try store.meta(id: "nope"))
    }

    // MARK: - 节 / 进度

    func testSectionsAndSectionProgressRoundTrip() throws {
        var meta = try store.importBook(source: source)
        XCTAssertFalse(meta.effectiveSections.isEmpty, "导入就该带节表")

        // 造两节，进度记在第 2 节第 42 个字
        meta.sections = [
            BookSection(index: 0, chapterIndex: 0, title: "第一节", fragment: "a",
                        start: 0, end: 10, charCount: 100),
            BookSection(index: 1, chapterIndex: 0, title: "第二节", fragment: "b",
                        start: 10, end: 20, charCount: 300),
        ]
        meta.progress = BookProgress(chapterIndex: 0, sectionIndex: 1, offset: 42, updatedAt: Date())
        try store.save(meta)

        let loaded = try XCTUnwrap(store.meta(id: meta.id))
        XCTAssertEqual(loaded.sections, meta.sections, "sections 必须能落盘再读回")
        XCTAssertEqual(loaded.progress?.sectionIndex, 1)
        XCTAssertEqual(loaded.progress?.offset, 42)
        XCTAssertEqual(loaded.section(at: 1)?.title, "第二节")
        // 进度条按节字数加权：第一节 100 + 第 2 节内 42，共 442 → ≈0.32
        XCTAssertEqual(loaded.progressFraction, 142.0 / 400.0, accuracy: 0.001)
    }

    func testSectionHTMLSlicesBody() throws {
        let meta = try store.importBook(source: source)
        let body = HTMLText.bodyHTML(try store.chapterHTML(id: meta.id, href: "OEBPS/Text/ch1.xhtml"))
        let total = body.utf16.count
        // 取正文里第一个 `</p>` 之后作为第二刀，验证切开后两段的文字分别对得上
        let cut = body.range(of: "</p>")!.upperBound.samePosition(in: body.utf16)!
        let cutOffset = body.utf16.distance(from: body.utf16.startIndex, to: cut)
        XCTAssertGreaterThan(cutOffset, 0)
        XCTAssertLessThan(cutOffset, total)

        let first = try store.sectionHTML(id: meta.id, chapterHref: "OEBPS/Text/ch1.xhtml",
                                          section: BookSection(index: 0, chapterIndex: 0, title: "",
                                                               fragment: nil, start: 0, end: cutOffset,
                                                               charCount: 1))
        let rest = try store.sectionHTML(id: meta.id, chapterHref: "OEBPS/Text/ch1.xhtml",
                                         section: BookSection(index: 1, chapterIndex: 0, title: "",
                                                              fragment: nil, start: cutOffset, end: total,
                                                              charCount: 1))
        XCTAssertEqual(first.html + rest.html, body, "两段切片拼回去必须等于原文（不丢字）")
        XCTAssertTrue(first.html.contains("第一段"))
    }

    func testLegacyZeroRangeSectionFallsBackToWholeChapter() throws {
        let meta = try store.importBook(source: source)
        // 老 meta 没有区间（start == end == 0）
        let whole = try store.sectionHTML(id: meta.id, chapterHref: "OEBPS/Text/ch1.xhtml",
                                          section: BookSection(index: 0, chapterIndex: 0, title: "",
                                                               fragment: nil, start: 0, end: 0,
                                                               charCount: 1))
        XCTAssertTrue(whole.html.contains("第一段"))
        XCTAssertGreaterThan(whole.html.utf16.count, 0)
    }

    func testAnnotationsRoundTrip() throws {
        let meta = try store.importBook(source: source)
        let item = BookAnnotation(
            id: "a1", chapterIndex: 0, quote: "第一段", prefix: "", suffix: "带",
            note: "想法", createdAt: Date(), updatedAt: Date()
        )
        try store.saveAnnotations([item], bookID: meta.id)
        let loaded = try store.annotations(bookID: meta.id)
        XCTAssertEqual(loaded.count, 1)
        XCTAssertEqual(loaded.first?.id, item.id)
        XCTAssertEqual(loaded.first?.quote, item.quote)
        XCTAssertEqual(loaded.first?.prefix, item.prefix)
        XCTAssertEqual(loaded.first?.suffix, item.suffix)
        XCTAssertEqual(loaded.first?.note, item.note)
        // meta/批注里的日期按 ISO8601 存（秒级），比较时给 1s 容差
        XCTAssertEqual(loaded.first?.createdAt.timeIntervalSince1970 ?? 0,
                       item.createdAt.timeIntervalSince1970, accuracy: 1)
    }

    func testCorruptAnnotationsThrowInsteadOfReturningEmpty() throws {
        let meta = try store.importBook(source: source)
        try Data("not json".utf8).write(to: store.bookDir(meta.id).appendingPathComponent("annotations.json"))
        XCTAssertThrowsError(try store.annotations(bookID: meta.id)) { thrown in
            guard case BookStoreError.readFailed = thrown else {
                return XCTFail("应报 readFailed，实际 \(thrown)")
            }
        }
    }

    func testBookmarksRoundTrip() throws {
        let meta = try store.importBook(source: source)
        let item = BookBookmark(id: "b1", chapterIndex: 0, sectionIndex: 1, offset: 42,
                                quote: "第一段", createdAt: Date())
        try store.saveBookmarks([item], bookID: meta.id)
        let loaded = try store.bookmarks(bookID: meta.id)
        XCTAssertEqual(loaded.count, 1)
        XCTAssertEqual(loaded.first?.sectionIndex, 1)
        XCTAssertEqual(loaded.first?.offset, 42)
        XCTAssertEqual(loaded.first?.quote, "第一段")
        // 没有 bookmarks.json = 还没有书签，不该报错
        try FileManager.default.removeItem(at: store.bookDir(meta.id).appendingPathComponent("bookmarks.json"))
        XCTAssertTrue(try store.bookmarks(bookID: meta.id).isEmpty)
    }

    func testCorruptBookmarksThrowInsteadOfReturningEmpty() throws {
        let meta = try store.importBook(source: source)
        try Data("not json".utf8).write(to: store.bookDir(meta.id).appendingPathComponent("bookmarks.json"))
        XCTAssertThrowsError(try store.bookmarks(bookID: meta.id)) { thrown in
            guard case BookStoreError.readFailed = thrown else {
                return XCTFail("应报 readFailed，实际 \(thrown)")
            }
        }
    }

    func testDeleteRemovesDirectoryAndIsIdempotent() throws {
        let meta = try store.importBook(source: source)
        try store.delete(id: meta.id)
        XCTAssertFalse(FileManager.default.fileExists(atPath: store.bookDir(meta.id).path))
        XCTAssertNoThrow(try store.delete(id: meta.id))
        XCTAssertTrue(try store.listBooks().books.isEmpty)
    }

    func testEmptyRootListsNothing() throws {
        let listing = try store.listBooks()
        XCTAssertTrue(listing.books.isEmpty)
        XCTAssertTrue(listing.failures.isEmpty, "根目录还不存在时不该报错")
    }
    // MARK: - 结构重算后的位置迁移

    func testMigrationMapsChapterOffsetsAcrossBoundaries() {
        let sections = [
            BookSection(index: 0, chapterIndex: 0, title: "", fragment: nil,
                        start: 0, end: 10, charCount: 100),
            BookSection(index: 1, chapterIndex: 0, title: "", fragment: nil,
                        start: 10, end: 20, charCount: 100),
            BookSection(index: 2, chapterIndex: 1, title: "", fragment: nil,
                        start: 0, end: 10, charCount: 50),
        ]
        let at = BookStructureMigration.chapterOffset(sectionIndex: 1, offset: 20, in: sections)
        XCTAssertEqual(at?.chapter, 0)
        XCTAssertEqual(at?.offset, 120, "章内偏移 = 前面各节字数 + 节内偏移")

        XCTAssertEqual(BookStructureMigration.locate(chapter: 0, offset: 120, in: sections)?.index, 1)
        XCTAssertEqual(BookStructureMigration.locate(chapter: 0, offset: 150, in: sections)?.offset, 50)
        XCTAssertNil(BookStructureMigration.locate(chapter: 9, offset: 0, in: sections))
        XCTAssertNil(BookStructureMigration.chapterOffset(sectionIndex: 9, offset: 0, in: sections))
    }

    func testRefreshMigratesAnnotationsAndProgress() throws {
        let meta = try store.importBook(source: source)

        // 造一份「旧结构」：章 0 被切成两节，进度和批注都在第 2 节
        var old = meta
        old.sections = [
            BookSection(index: 0, chapterIndex: 0, title: "", fragment: nil,
                        start: 0, end: 5, charCount: 100),
            BookSection(index: 1, chapterIndex: 0, title: "", fragment: nil,
                        start: 5, end: 10, charCount: 100),
            BookSection(index: 2, chapterIndex: 1, title: "", fragment: nil,
                        start: 0, end: 10, charCount: 100),
        ]
        old.progress = BookProgress(chapterIndex: 0, sectionIndex: 1, offset: 30, updatedAt: Date())
        old.parserVersion = BookStore.currentParserVersion - 1
        try store.save(old)
        let annotation = BookAnnotation(id: "a1", chapterIndex: 0, sectionIndex: 1,
                                        quote: "第一段", prefix: "", suffix: "", note: "",
                                        createdAt: Date(), updatedAt: Date())
        try store.saveAnnotations([annotation], bookID: meta.id)

        let refreshed = try store.refreshStructure(id: meta.id)
        XCTAssertEqual(refreshed?.parserVersion, BookStore.currentParserVersion)
        // 新结构里章 0 只有一节：旧的「第 2 节」偏移换算回来落在这一节
        XCTAssertEqual(refreshed?.progress?.sectionIndex, 0, "进度要跟着新节走，不能跳回书首")
        XCTAssertEqual(try store.annotations(bookID: meta.id).first?.sectionIndex, 0,
                       "批注不迁移 = 升级后划线全没了")
    }

    func testRefreshMigratesBookmarks() throws {
        let meta = try store.importBook(source: source)
        var old = meta
        old.sections = [
            BookSection(index: 0, chapterIndex: 0, title: "", fragment: nil,
                        start: 0, end: 5, charCount: 100),
            BookSection(index: 1, chapterIndex: 0, title: "", fragment: nil,
                        start: 5, end: 10, charCount: 100),
        ]
        old.parserVersion = BookStore.currentParserVersion - 1
        try store.save(old)
        let bookmark = BookBookmark(id: "b1", chapterIndex: 0, sectionIndex: 1, offset: 30,
                                    quote: "第一段", createdAt: Date())
        try store.saveBookmarks([bookmark], bookID: meta.id)

        _ = try store.refreshStructure(id: meta.id)
        let loaded = try store.bookmarks(bookID: meta.id).first
        XCTAssertEqual(loaded?.sectionIndex, 0, "书签也要跟着新节走")
        XCTAssertGreaterThan(loaded?.offset ?? 0, 0, "章内偏移要带过来，不能回到节首")
    }

}
