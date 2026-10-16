import XCTest
@testable import LocalArtifactsCore

/// 相邻「卡片类」产物并成一行（横向卡片条）；其余一条一行。
/// 这是分组的唯一事实来源：不看轮次、只看相邻——同一轮的产物已被 `LocalArtifactMerge` 排到一起。
final class LocalArtifactGroupingTests: XCTestCase {
    /// 用字符串当元素，'C' = 卡片，'x' = 普通消息
    private func rows(_ spec: String) -> [String] {
        LocalArtifactGrouping.rows(Array(spec.map(String.init))) { $0 == "C" }
            .map { $0.joined() }
    }

    func testEmpty() {
        XCTAssertEqual(rows(""), [])
    }

    func testAllPlain() {
        XCTAssertEqual(rows("xxx"), ["x", "x", "x"])
    }

    func testConsecutiveCardsMerge() {
        XCTAssertEqual(rows("CCC"), ["CCC"])
    }

    func testPlainMessageSplitsGroups() {
        XCTAssertEqual(rows("CCxCC"), ["CC", "x", "CC"])
    }

    func testSingleCardIsItsOwnRow() {
        XCTAssertEqual(rows("xCx"), ["x", "C", "x"])
    }

    func testLeadingAndTrailingCards() {
        XCTAssertEqual(rows("CxCCCx"), ["C", "x", "CCC", "x"])
    }

    /// 分组保序、不丢不重复
    func testKeepsOrderAndCount() {
        let spec = "CCxCCCxxCCC"
        let out = rows(spec)
        XCTAssertEqual(out.joined(), spec)
        XCTAssertEqual(out, ["CC", "x", "CCC", "x", "x", "CCC"])
    }
}
