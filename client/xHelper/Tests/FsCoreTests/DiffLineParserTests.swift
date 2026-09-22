import XCTest
@testable import FsCore

final class DiffLineParserTests: XCTestCase {

    private func kind(_ line: DiffLineParser.Line) -> String {
        switch line.kind {
        case .meta: return "meta"
        case .added: return "added"
        case .removed: return "removed"
        case .context: return "context"
        }
    }

    func testNumbersFollowHunkHeader() {
        let diff = """
        --- a/f.swift
        +++ b/f.swift
        @@ -134,4 +134,5 @@ struct S {
             .padding(16)
        -    .background(.regularMaterial)
        +    .background(Color("surfaceInput"))
        +    .overlay(RoundedRectangle(cornerRadius: 12))
             .padding(24)
        """
        let lines = DiffLineParser.parse(diff)
        // 文件头丢弃、空行丢弃：1 meta + 5 内容行
        XCTAssertEqual(lines.count, 6)
        XCTAssertEqual(lines.map(kind), ["meta", "context", "removed", "added", "added", "context"])

        XCTAssertNil(lines[0].number)
        XCTAssertEqual(lines[0].text, "@@ -134,4 +134,5 @@ struct S {")
        // 上下文行取新文件行号；前缀已剥
        XCTAssertEqual(lines[1].number, 134)
        XCTAssertEqual(lines[1].text, "    .padding(16)")
        // 删行取旧文件行号
        XCTAssertEqual(lines[2].number, 135)
        XCTAssertEqual(lines[2].text, "    .background(.regularMaterial)")
        // 加行取新文件行号（上下文已推进 newNo）
        XCTAssertEqual(lines[3].number, 135)
        XCTAssertEqual(lines[3].text, #"    .background(Color("surfaceInput"))"#)
        // 末行上下文：newNo 已被两个加行推进（134 → 137）
        XCTAssertEqual(lines[5].number, 137)
        XCTAssertEqual(lines[5].text, "    .padding(24)")
    }

    func testSecondHunkResyncsFromItsHeader() {
        let diff = """
        @@ -10,2 +10,2 @@
         kept
        -old
        +new
        @@ -200,1 +201,1 @@
        -tail
        +tail2
        """
        let lines = DiffLineParser.parse(diff)
        let tail = lines.suffix(2)
        XCTAssertEqual(tail.map(kind), ["removed", "added"])
        XCTAssertEqual(tail.first?.number, 200)
        XCTAssertEqual(tail.last?.number, 201)
    }

    func testNoNewlineMarkerAndNonUnifiedFallback() {
        for text in ["\\ No newline at end of file", "⚠️ 文件较大（9000 行 → 9001 行），未生成行级 diff 预览。"] {
            let lines = DiffLineParser.parse(text)
            XCTAssertEqual(lines.count, 1, text)
            XCTAssertEqual(kind(lines[0]), text.hasPrefix("\\") ? "meta" : "context", text)
            XCTAssertEqual(lines[0].number, text.hasPrefix("\\") ? nil : 1, text)
            XCTAssertEqual(lines[0].text, text)
        }
    }
}
