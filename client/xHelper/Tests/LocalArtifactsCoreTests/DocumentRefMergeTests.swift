import XCTest
@testable import LocalArtifactsCore

final class DocumentRefMergeTests: XCTestCase {
    private func patch(_ id: String, title: String? = nil, tags: [String]? = nil, preview: String? = nil) -> DocumentRefPatch {
        DocumentRefPatch(id: id, title: title, tags: tags, preview: preview)
    }

    func testMakeDefaultsTitleAndTags() {
        let ref = DocumentRef.make(from: patch("d"))
        XCTAssertEqual(ref.id, "d")
        XCTAssertEqual(ref.title, "文档")
        XCTAssertEqual(ref.tags, [])
        XCTAssertNil(ref.preview)
    }

    func testMergeNilFieldsKeepOld() {
        let base = DocumentRef(id: "d", title: "旧标题", tags: ["a", "b"], preview: "旧摘要")
        let merged = base.merged(with: patch("d"))
        XCTAssertEqual(merged, base, "全 nil 的 patch 不改动任何字段")
    }

    func testMergeOverridesProvidedFields() {
        let base = DocumentRef(id: "d", title: "旧标题", tags: ["a"], preview: "旧摘要")
        let merged = base.merged(with: patch("d", title: "新标题", tags: ["b"], preview: "新摘要"))
        XCTAssertEqual(merged, DocumentRef(id: "d", title: "新标题", tags: ["b"], preview: "新摘要"))
    }

    func testMergeEmptyTagsClears() {
        let base = DocumentRef(id: "d", title: "t", tags: ["a", "b"], preview: nil)
        let merged = base.merged(with: patch("d", tags: []))
        XCTAssertEqual(merged.tags, [], "显式空 = 清空")
    }

    func testScenarioCreateThenUpdateTags() {
        // create[a,b,c] → update[a] ⇒ [a]（修复卡片停在旧标签的 bug）
        var ref = DocumentRef.make(from: patch("d", title: "文档", tags: ["a", "b", "c"]))
        ref = ref.merged(with: patch("d", tags: ["a"]))
        XCTAssertEqual(ref.tags, ["a"])
    }

    func testScenarioUpdateWithoutTagsKeepsOld() {
        // create[a,b,c] → update(无 tags，仅改标题) ⇒ 标签保留
        var ref = DocumentRef.make(from: patch("d", title: "旧", tags: ["a", "b", "c"]))
        ref = ref.merged(with: patch("d", title: "新"))
        XCTAssertEqual(ref.tags, ["a", "b", "c"])
        XCTAssertEqual(ref.title, "新")
    }

    func testScenarioFragmentUpdateKeepsFullPreview() {
        // create(全文摘要) → update(局部替换，无 content ⇒ preview nil) ⇒ 旧摘要保留
        var ref = DocumentRef.make(from: patch("d", title: "t", tags: ["a"], preview: "这是全文摘要"))
        ref = ref.merged(with: patch("d", tags: ["a"]))
        XCTAssertEqual(ref.preview, "这是全文摘要")
    }

    func testScenarioUpdateClearsAllTags() {
        var ref = DocumentRef.make(from: patch("d", title: "t", tags: ["a", "b"]))
        ref = ref.merged(with: patch("d", tags: []))
        XCTAssertEqual(ref.tags, [])
    }

    func testMakeThenMergeDoesNotResetId() {
        let merged = DocumentRef.make(from: patch("d", tags: nil)).merged(with: patch("d", tags: ["x"]))
        XCTAssertEqual(merged.id, "d")
        XCTAssertEqual(merged.tags, ["x"])
    }
}
