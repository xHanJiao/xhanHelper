import XCTest
@testable import FsCore

final class GrepParserTests: XCTestCase {

    // MARK: - parseRgLine

    func testParseRgLine_MatchRecord() {
        let line = #"{"type":"match","data":{"path":{"text":"/a/b.go"},"lines":{"text":"  func main() {}"},"line_number":42}}"#
        let m = GrepParser.parseRgLine(line, maxLineChars: 2000)
        XCTAssertEqual(m?.file, "/a/b.go")
        XCTAssertEqual(m?.line, 42)
        XCTAssertEqual(m?.text, "  func main() {}")
    }

    func testParseRgLine_IgnoresNonMatchRecords() {
        let begin = #"{"type":"begin","data":{"path":{"text":"/a/b.go"}}}"#
        let summary = #"{"type":"summary","data":{"stats":{}}}"#
        XCTAssertNil(GrepParser.parseRgLine(begin, maxLineChars: 2000))
        XCTAssertNil(GrepParser.parseRgLine(summary, maxLineChars: 2000))
        XCTAssertNil(GrepParser.parseRgLine("garbage not json", maxLineChars: 2000))
    }

    func testParseRgLine_TruncatesLongLine() {
        let long = String(repeating: "x", count: 3000)
        let line = #"{"type":"match","data":{"path":{"text":"/a/b.go"},"lines":{"text":"\#(long)"},"line_number":1}}"#
        let m = GrepParser.parseRgLine(line, maxLineChars: 100)
        XCTAssertEqual(m?.text.count, 103) // 100 + "..."
        XCTAssertTrue(m!.text.hasSuffix("..."))
    }

    // MARK: - parsePlainLine

    func testParsePlainLine() {
        let m = GrepParser.parsePlainLine("/a/b.go:7:content:with:colons", maxLineChars: 2000)
        XCTAssertEqual(m?.file, "/a/b.go")
        XCTAssertEqual(m?.line, 7)
        XCTAssertEqual(m?.text, "content:with:colons")
    }

    func testParsePlainLine_Invalid() {
        XCTAssertNil(GrepParser.parsePlainLine("no-colons-here", maxLineChars: 2000))
        XCTAssertNil(GrepParser.parsePlainLine("/a/b.go:notanumber:x", maxLineChars: 2000))
    }

    // MARK: - GrepCollector

    func testCollector_Basic() {
        var c = GrepCollector()
        XCTAssertTrue(c.ingest(.init(file: "/a/1.go", line: 1, text: "a")))
        XCTAssertTrue(c.ingest(.init(file: "/a/2.go", line: 2, text: "b")))
        XCTAssertEqual(c.result.matches.count, 2)
        XCTAssertEqual(c.result.stop, .none)
    }

    func testCollector_IgnoresNilLines() {
        var c = GrepCollector()
        _ = c.ingest(nil)
        _ = c.ingest(nil)
        XCTAssertEqual(c.result.matches.count, 0)
    }

    func testCollector_MoreAvailableAtLimit() {
        var c = GrepCollector(maxResults: 3)
        for i in 0..<3 {
            XCTAssertTrue(c.ingest(.init(file: "/a/\(i).go", line: i, text: "x")))
        }
        // 第 4 条（maxResults+1）触发停止
        let cont = c.ingest(.init(file: "/a/4.go", line: 4, text: "x"))
        XCTAssertFalse(cont)
        XCTAssertEqual(c.result.matches.count, 3)
        XCTAssertEqual(c.result.stop, .moreAvailable)
    }

    func testCollector_ExactlyAtLimit_NoFalsePositive() {
        var c = GrepCollector(maxResults: 3)
        for i in 0..<3 {
            _ = c.ingest(.init(file: "/a/\(i).go", line: i, text: "x"))
        }
        XCTAssertEqual(c.result.matches.count, 3)
        XCTAssertEqual(c.result.stop, .none) // 恰好 100 不触发 moreAvailable
    }

    func testCollector_Skip() {
        var c = GrepCollector(maxResults: 100, skip: 2)
        for i in 0..<5 {
            _ = c.ingest(.init(file: "/a/\(i).go", line: i, text: "x"))
        }
        XCTAssertEqual(c.result.matches.map(\.line), [2, 3, 4])
    }

    func testCollector_SkipBeyondTotal_ReturnsEmpty() {
        var c = GrepCollector(maxResults: 100, skip: 10)
        for i in 0..<5 {
            _ = c.ingest(.init(file: "/a/\(i).go", line: i, text: "x"))
        }
        XCTAssertTrue(c.result.matches.isEmpty)
        XCTAssertEqual(c.result.stop, .none)
    }

    func testCollector_BudgetCappedMark() {
        var c = GrepCollector()
        c.markBudgetCapped()
        XCTAssertEqual(c.result.stop, .budgetCapped)
        XCTAssertTrue(c.result.truncated)
    }

    func testCollector_BudgetCappedDoesNotOverrideMoreAvailable() {
        var c = GrepCollector(maxResults: 1)
        _ = c.ingest(.init(file: "/a/1.go", line: 1, text: "x"))
        XCTAssertFalse(c.ingest(.init(file: "/a/2.go", line: 2, text: "x"))) // 触发 moreAvailable
        c.markBudgetCapped()
        XCTAssertEqual(c.result.stop, .moreAvailable) // 保留更精确的语义
    }

    func testCollector_SensitiveFilter() {
        var c = GrepCollector { file in file.contains(".env") }
        _ = c.ingest(.init(file: "/p/ok.go", line: 1, text: "x"))
        _ = c.ingest(.init(file: "/p/.env", line: 1, text: "SECRET=1"))
        XCTAssertEqual(c.result.matches.count, 1)
        XCTAssertEqual(c.result.omittedSensitive, 1)
    }

    func testCollector_SkipDoesNotCountSensitiveOmitted() {
        // skip 在敏感过滤之前：被过滤的匹配不占用 skip 计数
        var c = GrepCollector(maxResults: 100, skip: 1) { file in file.contains(".env") }
        _ = c.ingest(.init(file: "/p/.env", line: 1, text: "x")) // 敏感，省略
        _ = c.ingest(.init(file: "/p/1.go", line: 1, text: "x")) // 第 1 条有效匹配，被 skip
        _ = c.ingest(.init(file: "/p/2.go", line: 2, text: "x")) // 第 2 条有效匹配，返回
        XCTAssertEqual(c.result.matches.map(\.line), [2])
    }

    // MARK: - formatGrepOutput

    private func fmt(_ matches: [GrepMatch], omitted: Int = 0, stop: GrepStop = .none, pageSkip: Int = 0, maxBytes: Int = 1_000_000) -> (text: String, shownCount: Int, truncated: Bool) {
        GrepParser.formatGrepOutput(matches: matches, omittedSensitive: omitted, stop: stop, pageSkip: pageSkip, maxBytes: maxBytes)
    }

    func testFormat_NoMatches() {
        let out = fmt([])
        XCTAssertEqual(out.text, "No files found")
        XCTAssertEqual(out.shownCount, 0)
        XCTAssertFalse(out.truncated)
    }

    func testFormat_NoMatchesWithOmitted() {
        let out = fmt([], omitted: 3)
        XCTAssertTrue(out.text.contains("No files found"))
        XCTAssertTrue(out.text.contains("3 条匹配来自敏感文件已省略"))
    }

    func testFormat_MoreAvailable_HasSkipHint() {
        var matches: [GrepMatch] = []
        for i in 0..<100 {
            matches.append(.init(file: "/a/b.go", line: i, text: "x"))
        }
        let out = fmt(matches, stop: .moreAvailable)
        XCTAssertTrue(out.text.hasPrefix("Found 100 matches (more matches available. Use skip=100 to continue.)"))
        XCTAssertTrue(out.text.contains("/a/b.go:"))
        XCTAssertTrue(out.text.contains("Line 99"))
        XCTAssertEqual(out.shownCount, 100)
        XCTAssertTrue(out.truncated)
    }

    func testFormat_OmittedNote() {
        let matches = [GrepMatch(file: "/a/b.go", line: 1, text: "x")]
        let out = fmt(matches, omitted: 2)
        XCTAssertTrue(out.text.contains("2 条匹配来自敏感文件已省略"))
    }

    func testFormat_GroupedByFileSorted() {
        let matches = [
            GrepMatch(file: "/b/2.go", line: 1, text: "x"),
            GrepMatch(file: "/a/1.go", line: 1, text: "x"),
        ]
        let out = fmt(matches)
        let aIdx = out.text.range(of: "/a/1.go:")!.lowerBound
        let bIdx = out.text.range(of: "/b/2.go:")!.lowerBound
        XCTAssertLessThan(aIdx, bIdx, "文件应按名排序")
    }

    func testFormat_BudgetFit_CountIsShownAndSkipIsCumulative() {
        // 每条匹配 ~30 字节，预算 300 字节 → 只装得下 ~8 条
        var matches: [GrepMatch] = []
        for i in 0..<100 {
            matches.append(.init(file: "/a/b.go", line: i, text: String(repeating: "x", count: 20)))
        }
        let out = fmt(matches, pageSkip: 100, maxBytes: 300)
        XCTAssertLessThan(out.shownCount, 100)
        XCTAssertEqual(out.shownCount, out.text.components(separatedBy: "Line ").count - 1)
        XCTAssertTrue(out.truncated)
        XCTAssertTrue(out.text.contains("Use skip=\(100 + out.shownCount) to continue."), "skip 应为累计偏移")
        XCTAssertTrue(out.text.hasPrefix("Found \(out.shownCount) matches"), "count 应为显示条数")
    }

    func testFormat_BudgetCapped_StillHints() {
        let matches = [GrepMatch(file: "/a/b.go", line: 1, text: "x")]
        let out = fmt(matches, stop: .budgetCapped)
        XCTAssertTrue(out.truncated)
        XCTAssertTrue(out.text.contains("Use skip=1 to continue."))
    }

    func testFormat_ExactFitNoHint() {
        let matches = [GrepMatch(file: "/a/b.go", line: 1, text: "x")]
        let out = fmt(matches, stop: .none, maxBytes: 10_000)
        XCTAssertFalse(out.truncated)
        XCTAssertFalse(out.text.contains("skip="))
        XCTAssertTrue(out.text.hasPrefix("Found 1 matches\n"))
    }

    func testFormat_NextPagePageSkipHint() {
        var matches: [GrepMatch] = []
        for i in 0..<63 {
            matches.append(.init(file: "/a/b.go", line: i, text: "x"))
        }
        let out = fmt(matches, stop: .none, pageSkip: 100, maxBytes: 1_000_000)
        XCTAssertFalse(out.truncated, "全部装下且无更多 → 完整")
        XCTAssertEqual(out.shownCount, 63)
    }
}

final class SensitiveFileBlockerTests: XCTestCase {

    override func tearDown() {
        SensitiveFileBlocker.exemptPaths = []
        super.tearDown()
    }

    func testBlocksEnvFile() {
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/.env"))
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/.env.production"))
    }

    func testBlocksGitInternals() {
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/.git/config"))
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/.git/"))
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/.git"))
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/sub/.git/HEAD"))
    }

    func testAllowsNormalFiles() {
        XCTAssertNil(SensitiveFileBlocker.check("/Users/t/proj/main.go"))
        XCTAssertNil(SensitiveFileBlocker.check("/Users/t/proj/README.md"))
    }

    func testAllowsGitIgnore() {
        // .gitignore 文件名含 ".git" 但不属于 .git 内部
        XCTAssertNil(SensitiveFileBlocker.check("/Users/t/proj/.gitignore"))
    }

    func testBlocksKeyExtension() {
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/private.key"))
        XCTAssertNil(SensitiveFileBlocker.check("/Users/t/proj/public.pub"))
    }

    func testBlocksCredentialsPattern() {
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/credentials.json"))
    }

    func testExemptionShortCircuits() {
        let gitConfig = "/Users/t/proj/.git/config"
        XCTAssertNotNil(SensitiveFileBlocker.check(gitConfig))
        SensitiveFileBlocker.addExemption("/Users/t/proj/.git")
        XCTAssertNil(SensitiveFileBlocker.check(gitConfig))
        XCTAssertNil(SensitiveFileBlocker.check("/Users/t/proj/.git/HEAD"))
    }

    func testExemptionPrefixMatch() {
        SensitiveFileBlocker.addExemption("/Users/t/proj/.git")
        // 子路径也被豁免
        XCTAssertNil(SensitiveFileBlocker.check("/Users/t/proj/.git/objects/ab/cd"))
        // 兄弟目录不受影响
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/other/.git/config"))
    }

    func testExemptionDoesNotLeakToSiblingFile() {
        // 豁免 "…/proj/.git" 不影响 "…/proj/.git-credentials"（前缀匹配按路径组件）
        SensitiveFileBlocker.addExemption("/Users/t/proj/.git")
        XCTAssertNotNil(SensitiveFileBlocker.check("/Users/t/proj/.git-credentials"))
    }

    func testExemptionAddRemove() {
        SensitiveFileBlocker.addExemption("/Users/t/proj/.git")
        XCTAssertEqual(SensitiveFileBlocker.exemptPaths.count, 1)
        SensitiveFileBlocker.removeExemption("/Users/t/proj/.git")
        XCTAssertTrue(SensitiveFileBlocker.exemptPaths.isEmpty)
    }

    func testExemptionDedupe() {
        SensitiveFileBlocker.addExemption("/Users/t/proj/.git")
        SensitiveFileBlocker.addExemption("/Users/t/proj/.git")
        XCTAssertEqual(SensitiveFileBlocker.exemptPaths.count, 1)
    }
}
