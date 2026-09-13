import XCTest
@testable import FsCore

final class SubagentResultParserTests: XCTestCase {

    func testParseResearchResult() {
        let xml = """
        <research_result>
        <summary>DeepSeek 发布了 V4 系列模型。</summary>
        <points>Flash 主打快速响应；Pro 支持思考模式</points>
        <sources>官方博客 | https://example.com | 高；社区讨论 | https://b.com | 中</sources>
        <failed_attempts>无</failed_attempts>
        <open_questions>具体价格未公布</open_questions>
        </research_result>
        """
        let r = SubagentResultParser.parse(xml)
        XCTAssertEqual(r?.title, "网页调研")
        let md = r?.markdown ?? ""
        XCTAssertTrue(md.contains("### 结论\n\nDeepSeek 发布了 V4 系列模型。"))
        XCTAssertTrue(md.contains("### 要点\n\n- Flash 主打快速响应\n- Pro 支持思考模式"))
        XCTAssertTrue(md.contains("- 官方博客 | https://example.com | 高"))
        XCTAssertFalse(md.contains("失败尝试"), "值为「无」的小节应跳过")
        XCTAssertTrue(md.contains("### 未解决问题\n\n- 具体价格未公布"))
    }

    func testParseFileExploreResult() {
        let xml = """
        <file_explore_result>
        <summary>找到了 2 处调用点。</summary>
        <findings>/a/x.go:12 调用 foo；/a/y.go:30 调用 bar</findings>
        <files_read>/a/x.go
        /a/y.go</files_read>
        <open_questions>无</open_questions>
        </file_explore_result>
        """
        let r = SubagentResultParser.parse(xml)
        XCTAssertEqual(r?.title, "文件检索")
        let md = r?.markdown ?? ""
        XCTAssertTrue(md.contains("### 发现\n\n- /a/x.go:12 调用 foo\n- /a/y.go:30 调用 bar"))
        XCTAssertTrue(md.contains("### 已读文件\n\n- /a/x.go\n- /a/y.go"))
        XCTAssertFalse(md.contains("未解决问题"))
    }

    func testParseUserContextResult() {
        let xml = """
        <user_context_result>
        <summary>用户偏好 Go。</summary>
        <findings>记忆：常用 Go；笔记：有 Go 学习笔记</findings>
        <open_questions>无</open_questions>
        </user_context_result>
        """
        let r = SubagentResultParser.parse(xml)
        XCTAssertEqual(r?.title, "用户信息检索")
        XCTAssertTrue(r?.markdown.contains("### 发现") == true)
    }

    func testParseMalformedReturnsNil() {
        XCTAssertNil(SubagentResultParser.parse("就是一段普通文本，没有 XML"))
        XCTAssertNil(SubagentResultParser.parse("<other_result><summary>x</summary></other_result>"))
        XCTAssertNil(SubagentResultParser.parse("<research_result></research_result>"))
    }

    func testCleanSummaryExtractsClosedTag() {
        let xml = "<research_result><summary>结论在此</summary><points>a；b</points></research_result>"
        XCTAssertEqual(SubagentResultParser.cleanSummary(xml), "结论在此")
    }

    func testCleanSummaryStripsTruncatedXml() {
        // 模拟服务端 200 字截断：summary 未闭合（无 </summary>）、尾部有半个标签
        let truncated = "<research_result>\n<summary>这是一个很长的结论，还没有结束就被截断了<points>要点一；要点二</points>\n<po"
        let out = SubagentResultParser.cleanSummary(truncated)
        XCTAssertFalse(out.contains("<"))
        XCTAssertFalse(out.contains(">"))
        XCTAssertTrue(out.contains("这是一个很长的结论"))
        XCTAssertTrue(out.contains("要点一"))
    }

    func testCleanSummaryPlainTextUnchanged() {
        XCTAssertEqual(SubagentResultParser.cleanSummary("超时失败，已产出部分结果"), "超时失败，已产出部分结果")
    }
}
