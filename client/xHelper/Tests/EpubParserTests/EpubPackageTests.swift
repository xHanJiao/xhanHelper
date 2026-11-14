import XCTest
@testable import EpubCore

/// 解析器单测：自造 fixture 打包成真 epub，走与线上完全相同的解包/解析路径。
final class EpubPackageTests: XCTestCase {

    // MARK: - 路径归一

    func testNormalizeKeepsInsideRoot() {
        XCTAssertEqual(EpubArchive.normalize("Text/ch1.xhtml", baseDir: "OEBPS"), "OEBPS/Text/ch1.xhtml")
        XCTAssertEqual(EpubArchive.normalize("ch1.xhtml", baseDir: "OEBPS/Text"), "OEBPS/Text/ch1.xhtml")
        XCTAssertEqual(EpubArchive.normalize("./ch1.xhtml", baseDir: "OEBPS"), "OEBPS/ch1.xhtml")
        XCTAssertEqual(EpubArchive.normalize("Text/../ch1.xhtml", baseDir: "OEBPS"), "OEBPS/ch1.xhtml")
        XCTAssertEqual(EpubArchive.normalize("/OEBPS/ch1.xhtml", baseDir: "OEBPS/Text"), "OEBPS/ch1.xhtml")
    }

    func testNormalizeStripsFragmentAndQuery() {
        XCTAssertEqual(EpubArchive.normalize("Text/ch1.xhtml#sec", baseDir: "OEBPS"), "OEBPS/Text/ch1.xhtml")
        XCTAssertEqual(EpubArchive.normalize("Text/ch1.xhtml?x=1", baseDir: "OEBPS"), "OEBPS/Text/ch1.xhtml")
        XCTAssertEqual(EpubArchive.normalize("Text/a%20b.xhtml", baseDir: "OEBPS"), "OEBPS/Text/a b.xhtml")
    }

    func testNormalizeRejectsEscapeAndEmpty() {
        // 从 OEBPS 往上一级仍在 content/ 内（`../Images/x.jpg` 是合法写法）
        XCTAssertEqual(EpubArchive.normalize("../outside.xhtml", baseDir: "OEBPS"), "outside.xhtml")
        XCTAssertNil(EpubArchive.normalize("../../outside.xhtml", baseDir: "OEBPS"))
        XCTAssertNil(EpubArchive.normalize("../../etc/passwd", baseDir: ""))
        XCTAssertNil(EpubArchive.normalize("#frag", baseDir: "OEBPS"))
        XCTAssertNil(EpubArchive.normalize("", baseDir: ""))
        XCTAssertNil(EpubArchive.normalize("/../x", baseDir: ""))
    }

    // MARK: - EPUB2 / NCX

    func testEPUB2ParsesSpineNcxAndCover() throws {
        let url = try SampleBook.epub2()
        addTeardownBlock { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }

        let contentDir = EpubFixture.tempDir()
        addTeardownBlock { try? FileManager.default.removeItem(at: contentDir) }
        try EpubArchive.unzip(url, into: contentDir)
        let package = try EpubPackage.parse(contentDir: contentDir)

        XCTAssertEqual(package.title, "旧格式 & 目录")
        XCTAssertEqual(package.author, "某作者")
        XCTAssertEqual(package.language, "zh-CN")
        XCTAssertEqual(package.chapters.count, 2)
        XCTAssertEqual(package.chapters[0].href, "OEBPS/Text/ch1.xhtml")
        XCTAssertEqual(package.chapters[1].href, "OEBPS/Text/ch2.xhtml")
        XCTAssertEqual(package.chapters[0].title, "第一章 起", "目录标题应覆盖内文标题")
        XCTAssertEqual(package.chapters[1].title, "第二章 承")
        XCTAssertGreaterThan(package.chapters[0].charCount, 0)
        XCTAssertEqual(package.coverPath, "OEBPS/Images/cover.jpg")

        XCTAssertEqual(package.toc.map(\.chapterIndex), [0, 1, 1])
        XCTAssertEqual(package.toc.map(\.depth), [0, 0, 1])
        XCTAssertEqual(package.toc.map(\.title), ["第一章 起", "第二章 承", "2.1 小节"])
        XCTAssertEqual(package.toc.map(\.fragment), [nil, nil, "sec"], "锚点要留下来，否则跳转只能落到文件开头")
    }

    // MARK: - 一文件装整本书（目录全靠锚点）

    func testRichTOCOverSingleSpineFileKeepsFragmentsAndDropsEmptyTitles() throws {
        let url = try SampleBook.singleFileRichTOC()
        addTeardownBlock { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }

        let contentDir = EpubFixture.tempDir()
        addTeardownBlock { try? FileManager.default.removeItem(at: contentDir) }
        try EpubArchive.unzip(url, into: contentDir)
        let package = try EpubPackage.parse(contentDir: contentDir)

        XCTAssertEqual(package.chapters.count, 1)
        // 空标题的分组包装层（<li><ol>…</ol></li>）不占行，子条目照常保留
        XCTAssertEqual(package.toc.map(\.title), ["第 1 章 开头", "第 2 章 中间", "第 3 章 结尾"])
        XCTAssertEqual(package.toc.map(\.fragment), ["c1", "c2", "c3"])
        XCTAssertEqual(package.toc.map(\.chapterIndex), [0, 0, 0])
        // 目录本身是平的（nav 只套了一层空包装），层级由锚点所在标题推出来：h1 → 0，h2 → 1
        XCTAssertEqual(package.toc.map(\.depth), [0, 1, 1])
        XCTAssertEqual(package.toc.map(\.key), ["0:c1:0:第 1 章 开头", "0:c2:1:第 2 章 中间", "0:c3:1:第 3 章 结尾"])
    }

    // MARK: - 目录层级

    func testScanBodyMapsAnchorsToHeadingLevels() {
        let body = """
        <h1 id="a">一</h1><h2 id='b'>二</h2><h3 name="c">三</h3>
        <p id="d">段</p><div id="e"><h4 id="f">四</h4></div>
        <h2><a id="g">标题里的锚点</a></h2>
        <!-- <h1 id="comment">注释里的假标签</h1> -->
        <span data-id="fake">骗你的</span><img filename="x.png"/>
        <H5 ID="upper">大写属性</H5>
        <h1><h2 id="nest">嵌套</h2><a id="after">内层闭合后</a></h1>
        <script>if (a < b) { var s = '<h1 id="js">假标题</h1>'; }</script>
        <p id="tail">尾巴</p>
        """
        let scanned = HTMLText.scanBody(body)
        XCTAssertEqual(scanned.anchors["a"]?.headingLevel, 1)
        XCTAssertEqual(scanned.anchors["b"]?.headingLevel, 2, "单引号 id 也要认")
        XCTAssertEqual(scanned.anchors["c"]?.headingLevel, 3, "name= 锚点同样认")
        XCTAssertNil(scanned.anchors["d"]?.headingLevel, "锚点在 <p> 上不猜层级")
        XCTAssertNil(scanned.anchors["e"]?.headingLevel, "锚点在容器上不猜层级")
        XCTAssertEqual(scanned.anchors["f"]?.headingLevel, 4)
        XCTAssertEqual(scanned.anchors["g"]?.headingLevel, 2, "id 挂在标题内的 <a> 上也算标题层级")
        XCTAssertNil(scanned.anchors["comment"], "注释里的假标签不算锚点")
        XCTAssertNil(scanned.anchors["fake"], "data-id 不是 id")
        XCTAssertNil(scanned.anchors["fake-x"], "filename 不是 name")
        XCTAssertEqual(scanned.anchors["upper"]?.headingLevel, 5, "大写标签名 / 大写属性名也要认")
        XCTAssertEqual(scanned.anchors["nest"]?.headingLevel, 2)
        XCTAssertEqual(scanned.anchors["after"]?.headingLevel, 1, "内层标题闭合后要回到外层（标题栈）")
        XCTAssertNil(scanned.anchors["js"], "script 里的假标签不认")
        XCTAssertNil(scanned.anchors["tail"]?.headingLevel)
        XCTAssertEqual(scanned.firstHeadingLevel, 1)
        XCTAssertNil(HTMLText.scanBody("<p>没有标题</p>").firstHeadingLevel)
    }

    func testSectionPlanFindsAnchorsNestedInHeadings() {
        // 每段约 1300 字 × 40 段 → 超过 3 万字上限，必须按锚点切两节
        let para = String(repeating: "填充正文用来超过切节阈值。", count: 100)
        var body = "<h1 id=\"c1\">第一章</h1>"
        for _ in 1...20 { body += "<p>\(para)</p>" }
        body += "<h2><a id=\"mid\">中间</a></h2>"
        for _ in 1...20 { body += "<p>\(para)</p>" }

        let plan = HTMLText.planSections(body: body, anchors: [("c1", "第一章"), ("mid", "中间")])
        XCTAssertEqual(plan.sections.count, 2)
        XCTAssertEqual(plan.sectionByFragment["mid"], 1, "id 挂在标题内的 <a> 上也要能定位到节")
        XCTAssertEqual(plan.headingLevelByFragment["mid"], 2)
        XCTAssertEqual(plan.firstHeadingLevel, 1)
    }

    func testPartTitleDetection() {
        XCTAssertTrue(HTMLText.isPartTitle("第一部分　设计原则"))
        XCTAssertTrue(HTMLText.isPartTitle("第 12 部分 实践"))
        XCTAssertTrue(HTMLText.isPartTitle("Part II"))
        XCTAssertTrue(HTMLText.isPartTitle("Part 1: 基础"))
        XCTAssertTrue(HTMLText.isPartTitle("上篇 基础"))
        XCTAssertFalse(HTMLText.isPartTitle("第 1 章 引言"))
        XCTAssertFalse(HTMLText.isPartTitle("最好部分设计原则"), "不是「第 N 部分」就不算分组标题")
        XCTAssertFalse(HTMLText.isPartTitle("Part 1 of the story"), "序号后面不是分隔符就不算分组标题")
        XCTAssertTrue(HTMLText.endsPartGroup("附录 A：术语表"))
        XCTAssertTrue(HTMLText.endsPartGroup("致谢词"))
        XCTAssertTrue(HTMLText.endsPartGroup("Acknowledgements"))
        XCTAssertFalse(HTMLText.endsPartGroup("Indexing for speed"), "别把 Indexing 当索引")
        XCTAssertFalse(HTMLText.endsPartGroup("第 1 章 引言"))
    }

    func testFlatTOCDerivesDepthFromHeadingsAndParts() throws {
        let url = try SampleBook.flatTOCHierarchy()
        addTeardownBlock { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        let contentDir = EpubFixture.tempDir()
        addTeardownBlock { try? FileManager.default.removeItem(at: contentDir) }
        try EpubArchive.unzip(url, into: contentDir)
        let package = try EpubPackage.parse(contentDir: contentDir)

        XCTAssertEqual(package.toc.map(\.depth), [0, 1, 2, 3, 0, 1],
                       "h1 部分是 0；其后的章 +1；h2/h3 各再深一级；附录退出分组回到 0；"
                       + "不带锚点的条目按该章首个标题（h2）推一级")
        XCTAssertEqual(package.toc.map(\.title),
                       ["第一部分 设计原则", "第 1 章 引言", "约书亚树", "一点说明", "附录 A：术语表", "续篇"])
        XCTAssertEqual(package.chapters.count, 2)
        XCTAssertEqual(package.toc.last?.chapterIndex, 1)
        // 切节不受影响：前五条目录都指向同一节的锚点
        XCTAssertEqual(package.sections.count, 2, "两章各一节")
        XCTAssertEqual(package.toc.compactMap(\.sectionIndex), [0, 0, 0, 0, 0, 1])
    }

    // MARK: - 分节（大章按锚点切「节」）

    func testTopLevelRangesCoverBodyAndRespectNesting() {
        let body = #"""
        <div><p>a</p><p>b</p></div>
        <!-- 注释里的 <div> 不算 -->
        <p title="a>b">c</p>
        <br/>
        <svg><image xlink:href="x.png"/></svg>
        tail
        """#
        let ranges = HTMLText.topLevelRanges(body)
        // 区间必须连续覆盖整个 body，切片才不会丢字
        XCTAssertEqual(ranges.first?.range.lowerBound, 0)
        XCTAssertEqual(ranges.last?.range.upperBound, body.utf16.count)
        for pair in zip(ranges, ranges.dropFirst()) {
            XCTAssertEqual(pair.0.range.upperBound, pair.1.range.lowerBound)
        }
        // 顶层片段 = div / 注释后的 p / br / svg / tail，注释不单独成段
        XCTAssertEqual(ranges.count, 5, "顶层片段数不符：\(ranges.count)")
        XCTAssertGreaterThan(ranges[0].textCount, 0)
    }

    func testSectionPlanSplitsBigChapterByAnchors() {
        // 每段约 1200 字 × 40 段 = 48000 字 → 超过 3 万上限，必须切
        let para = String(repeating: "这一段用来把字数撑过切节阈值。", count: 100)
        var body = "<h1 id=\"c1\">第一章</h1>"
        for i in 1...40 {
            body += "<p id=\"p\(i)\" ->\(para)</p>".replacingOccurrences(of: " ->", with: ">")
        }
        let anchors = [("c1", "第一章"), ("p10", "第十段"), ("p30", "第三十段")]
        let plan = HTMLText.planSections(body: body, anchors: anchors)

        XCTAssertEqual(plan.sections.count, 3, "三个锚点 → 三节")
        XCTAssertEqual(plan.sectionByFragment["c1"], 0)
        XCTAssertEqual(plan.sectionByFragment["p10"], 1)
        XCTAssertEqual(plan.sectionByFragment["p30"], 2)
        // 区间首尾相接、覆盖整个 body
        XCTAssertEqual(plan.sections.first?.start, 0)
        XCTAssertEqual(plan.sections.last?.end, body.utf16.count)
        for pair in zip(plan.sections, plan.sections.dropFirst()) {
            XCTAssertEqual(pair.0.end, pair.1.start)
        }
        XCTAssertGreaterThan(plan.sections[1].charCount, 0)
    }

    func testSectionPlanKeepsSmallChapterWhole() {
        let body = "<h1 id=\"a\">一</h1><p>短章。</p><h2 id=\"b\">二</h2><p>也很短。</p>"
        let plan = HTMLText.planSections(body: body, anchors: [("a", "一"), ("b", "二")])
        XCTAssertEqual(plan.sections.count, 1, "字数没到上限就不切，行为与切分前一致")
        XCTAssertEqual(plan.sectionByFragment["a"], 0)
        XCTAssertEqual(plan.sectionByFragment["b"], 0)
    }

    func testSectionPlanWithoutAnchorsChunksBySize() {
        let para = String(repeating: "无目录的大书只能按字数切。", count: 100)
        let body = (1...40).map { _ in "<p>\(para)</p>" }.joined()
        let plan = HTMLText.planSections(body: body, anchors: [])
        XCTAssertGreaterThan(plan.sections.count, 1, "没有锚点也要切，否则一节就是整本书")
        XCTAssertEqual(plan.sections.first?.start, 0)
        XCTAssertEqual(plan.sections.last?.end, body.utf16.count)
    }

    func testRichTOCSingleFileMapsTOCToSections() throws {
        let url = try SampleBook.singleFileRichTOC()
        addTeardownBlock { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        let contentDir = EpubFixture.tempDir()
        addTeardownBlock { try? FileManager.default.removeItem(at: contentDir) }
        try EpubArchive.unzip(url, into: contentDir)
        let package = try EpubPackage.parse(contentDir: contentDir)

        // 这本书正文很短 → 只有一节；三条目录都指向它
        XCTAssertEqual(package.sections.count, 1)
        XCTAssertEqual(package.toc.compactMap(\.sectionIndex), [0, 0, 0])
        XCTAssertEqual(package.sections[0].start, 0)
        XCTAssertGreaterThan(package.sections[0].end, 0)
    }

    // MARK: - EPUB3 / nav

    func testEPUB3ParsesNavWithNamedEntities() throws {
        let url = try SampleBook.epub3()
        addTeardownBlock { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }

        let contentDir = EpubFixture.tempDir()
        addTeardownBlock { try? FileManager.default.removeItem(at: contentDir) }
        try EpubArchive.unzip(url, into: contentDir)
        let package = try EpubPackage.parse(contentDir: contentDir)

        XCTAssertEqual(package.title, "新格式书")
        XCTAssertEqual(package.chapters.map(\.href), ["OEBPS/text/c1.xhtml", "OEBPS/text/c2.xhtml"])
        XCTAssertEqual(package.toc.count, 3, "landmarks 里的条目不该混进目录")
        XCTAssertEqual(package.toc.map(\.depth), [0, 0, 1])
        XCTAssertEqual(package.toc[0].title, "第一章\u{00A0}开端 & 续", "&nbsp; / &amp; 都要变成字符")
        XCTAssertEqual(package.toc[1].title, "第二章\u{2014}继续", "&mdash; 要变成字符")
        XCTAssertEqual(package.toc[2].chapterIndex, 1, "带 #fragment 的目录项要落回同一章")
        XCTAssertNil(package.coverPath)
    }

    // MARK: - 无章节 / 各种拒绝

    func testSingleChapterBookHasNoChapterNavigation() throws {
        let meta = try importMeta(SampleBook.singleChapter())
        XCTAssertEqual(meta.chapterCount, 1)
        XCTAssertTrue(meta.toc.isEmpty)
        XCTAssertFalse(meta.hasChapterNavigation, "TOC 空且 spine=1 → 当无章节书")
    }

    func testMultiSpineWithoutTOCStillHasNavigation() {
        let meta = BookMeta(
            id: "x", title: "t", author: nil, language: nil, chapterCount: 3,
            chapters: (0..<3).map { BookChapter(index: $0, href: "c\($0).xhtml", title: "c\($0)", charCount: 10) },
            toc: [], coverFile: nil, sourceBytes: 1, sourceSHA256: "s",
            importedAt: Date(), lastReadAt: nil, progress: nil
        )
        XCTAssertTrue(meta.hasChapterNavigation, "多章即使没目录也算按章导航")
    }

    func testPartialTOCStillKeepsChapterNavigation() {
        let meta = BookMeta(
            id: "x", title: "t", author: nil, language: nil, chapterCount: 3,
            chapters: (0..<3).map { BookChapter(index: $0, href: "c\($0).xhtml", title: "c\($0)", charCount: 10) },
            toc: [BookTOCItem(title: "Part I", chapterIndex: 0, depth: 0)],
            coverFile: nil, sourceBytes: 1, sourceSHA256: "s",
            importedAt: Date(), lastReadAt: nil, progress: nil
        )
        XCTAssertTrue(meta.hasChapterNavigation, "坏目录只列了 1 章，不能把第 2、3 章藏起来")
    }

    func testSpineItemsThatAreNotChaptersAreSkipped() throws {
        let meta = try importMeta(SampleBook.spineWithImage())
        XCTAssertEqual(meta.chapterCount, 1, "封面图 + 缺失项都跳过，只留真章节")
        XCTAssertEqual(meta.chapters[0].href, "OEBPS/c1.xhtml")
        XCTAssertEqual(meta.chapters[0].index, 0)
    }

    func testUncompressedSizeIsMeasured() throws {
        let url = try SampleBook.epub2()
        addTeardownBlock { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        let total = EpubArchive.totalUncompressedBytes(url)
        XCTAssertNotNil(total, "zip 列表要能解析出总大小（zip bomb 上限靠它）")
        XCTAssertGreaterThan(total ?? 0, 0)
        XCTAssertLessThan(total ?? .max, 10 * 1024 * 1024)
    }

    func testDRMIsRejected() throws {
        try assertImportFails(SampleBook.drm()) { error in
            XCTAssertEqual(error, .drm)
        }
    }

    func testFixedLayoutIsRejected() throws {
        try assertImportFails(SampleBook.fixedLayout()) { error in
            XCTAssertEqual(error, .fixedLayout)
        }
    }

    func testEmptySpineIsRejected() throws {
        try assertImportFails(SampleBook.emptySpine()) { error in
            XCTAssertEqual(error, .noContent)
        }
    }

    func testInvalidArchiveIsRejected() throws {
        let junk = EpubFixture.tempDir().appendingPathComponent("junk.epub")
        try Data("not a zip".utf8).write(to: junk)
        try assertImportFails(junk) { error in
            guard case .invalidFile = error else {
                return XCTFail("应报 invalidFile，实际 \(error)")
            }
        }
    }

    func testZipSlipIsRejectedBeforeExtracting() throws {
        let url = try SampleBook.zipSlip()
        let store = BookStore(root: EpubFixture.tempDir())
        XCTAssertThrowsError(try store.importBook(source: url)) { thrown in
            guard let error = thrown as? BookImportError, case .invalidFile(let why) = error else {
                return XCTFail("应报 invalidFile，实际 \(thrown)")
            }
            XCTAssertTrue(why.contains(".."), "报错要指出越界条目：\(why)")
        }
        let leftovers = try FileManager.default.contentsOfDirectory(at: store.root, includingPropertiesForKeys: nil)
        XCTAssertTrue(leftovers.isEmpty, "导入失败要清掉半成品目录")
    }

    // MARK: - 工具

    private func importMeta(_ url: URL) throws -> BookMeta {
        let store = BookStore(root: EpubFixture.tempDir())
        addTeardownBlock { try? FileManager.default.removeItem(at: store.root) }
        return try store.importBook(source: url)
    }

    private func assertImportFails(_ url: URL, file: StaticString = #filePath, line: UInt = #line,
                                  check: (BookImportError) -> Void) throws {
        let store = BookStore(root: EpubFixture.tempDir())
        addTeardownBlock { try? FileManager.default.removeItem(at: store.root) }
        XCTAssertThrowsError(try store.importBook(source: url), file: file, line: line) { thrown in
            guard let error = thrown as? BookImportError else {
                return XCTFail("应抛 BookImportError，实际 \(thrown)", file: file, line: line)
            }
            check(error)
        }
        let leftovers = try FileManager.default.contentsOfDirectory(at: store.root, includingPropertiesForKeys: nil)
        XCTAssertTrue(leftovers.isEmpty, "失败后不该留下目录", file: file, line: line)
    }
}
