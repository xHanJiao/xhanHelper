import XCTest
@testable import LocalArtifactsCore

final class DocumentArtifactTests: XCTestCase {
    func testCreateUsesArgsForTagsAndPreview() {
        let patch = DocumentArtifactProducer.patch(
            function: "create_document",
            args: ["title": "草稿", "content": "第一行\n\n第二行   内容", "tags": "技术, Go ,技术"],
            data: ["id": "doc-1", "title": "草稿", "message": "文档已保存"]
        )
        XCTAssertEqual(patch?.id, "doc-1")
        XCTAssertEqual(patch?.title, "草稿")
        XCTAssertEqual(patch?.tags, ["技术", "Go"], "近空格 + 去重")
        XCTAssertEqual(patch?.preview, "第一行 第二行 内容", "折叠换行/连续空白")
    }

    func testCreateWithoutTagsYieldsEmptyArray() {
        let patch = DocumentArtifactProducer.patch(
            function: "create_document",
            args: ["title": "无标签", "content": "正文"],
            data: ["id": "doc-1", "title": "无标签"]
        )
        XCTAssertEqual(patch?.tags, [], "新建无 tags 参数 = 空标签")
    }

    func testCreateTitleFromResult() {
        let patch = DocumentArtifactProducer.patch(
            function: "create_document",
            args: ["title": "入参标题", "content": "x"],
            data: ["id": "d", "title": "返回标题"]
        )
        XCTAssertEqual(patch?.title, "返回标题")
    }

    func testUpdateTagsProvided() {
        let patch = DocumentArtifactProducer.patch(
            function: "update_document",
            args: ["id": "doc-3", "title": "改名", "tags": "回归"],
            data: ["id": "doc-3", "message": "文档已更新"]
        )
        XCTAssertEqual(patch?.id, "doc-3")
        XCTAssertEqual(patch?.title, "改名")
        XCTAssertEqual(patch?.tags, ["回归"])
    }

    func testUpdateTagsAbsentMeansNil() {
        let patch = DocumentArtifactProducer.patch(
            function: "update_document",
            args: ["id": "doc-3", "content": "全文重写"],
            data: ["id": "doc-3"]
        )
        XCTAssertNil(patch?.tags, "未带 tags 参数 = 未提供（合并时保留旧值）")
        XCTAssertEqual(patch?.preview, "全文重写")
    }

    func testUpdateEmptyTagsMeansExplicitClear() {
        let patch = DocumentArtifactProducer.patch(
            function: "update_document",
            args: ["id": "doc-3", "tags": ""],
            data: ["id": "doc-3"]
        )
        XCTAssertEqual(patch?.tags, [], "空串 = 显式清空")
    }

    func testUpdateDoesNotUseNewTextAsPreview() {
        let patch = DocumentArtifactProducer.patch(
            function: "update_document",
            args: ["id": "doc-3", "old_text": "旧", "new_text": "替换后的片段"],
            data: ["id": "doc-3"]
        )
        XCTAssertNil(patch?.preview, "局部替换的 new_text 是片段，不作摘要")
    }

    func testGetReturnsNil() {
        let patch = DocumentArtifactProducer.patch(
            function: "get_document",
            args: ["id": "doc-2"],
            data: ["id": "doc-2", "title": "已存在", "tags": ["检索"], "content": "正文"]
        )
        XCTAssertNil(patch, "读取不出卡片（避免过期快照 + 写后被跳过）")
    }

    func testMissingOrEmptyIdReturnsNil() {
        XCTAssertNil(DocumentArtifactProducer.patch(function: "create_document", args: ["title": "x"], data: ["title": "x"]))
        XCTAssertNil(DocumentArtifactProducer.patch(function: "create_document", args: [:], data: ["id": ""]))
        XCTAssertNil(DocumentArtifactProducer.patch(function: "create_document", args: [:], data: nil))
    }

    func testNonSingleDocumentFunctionsReturnNil() {
        for fn in ["query_documents", "list_documents", "list_tags", "delete_document"] {
            XCTAssertNil(DocumentArtifactProducer.patch(function: fn, args: [:], data: ["id": "x"]), "\(fn) 不应出卡片")
        }
    }

    func testPreviewTruncatedToMaxLen() {
        let long = String(repeating: "字", count: 200)
        XCTAssertEqual(DocumentArtifactProducer.makePreview(long)?.count, DocumentArtifactProducer.previewMaxLen)
    }

    func testPreviewNilForBlankContent() {
        XCTAssertNil(DocumentArtifactProducer.makePreview(nil))
        XCTAssertNil(DocumentArtifactProducer.makePreview("   \n\t  "))
    }

    func testPreviewCollapsesWhitespace() {
        XCTAssertEqual(DocumentArtifactProducer.makePreview("a\nb"), "a b")
        XCTAssertEqual(DocumentArtifactProducer.makePreview("  a   b  "), "a b")
    }

    func testParseTagsSplitsBothCommasAndDedupes() {
        XCTAssertEqual(DocumentArtifactProducer.parseTags("a，b, a , ,c"), ["a", "b", "c"])
        XCTAssertEqual(DocumentArtifactProducer.parseTags(""), [])
        XCTAssertEqual(DocumentArtifactProducer.parseTags(nil), [])
    }
}
