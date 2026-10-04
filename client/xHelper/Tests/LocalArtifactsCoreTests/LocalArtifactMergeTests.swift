import XCTest
@testable import LocalArtifactsCore

final class LocalArtifactMergeTests: XCTestCase {
    private struct Item: Equatable {
        let id: String
        let role: String
    }

    private func reposition(_ items: [Item]) -> [Item] {
        LocalArtifactMerge.reposition(
            items,
            isLocal: { $0.role == "local" },
            isUser: { $0.role == "user" },
            isAssistant: { $0.role == "assistant" }
        )
    }

    func testLocalAlreadyBeforeAssistantStays() {
        let items = [Item(id: "u", role: "user"), Item(id: "l", role: "local"), Item(id: "a", role: "assistant")]
        XCTAssertEqual(reposition(items).map(\.id), ["u", "l", "a"])
    }

    func testLocalAfterAssistantMovesBeforeIt() {
        let items = [Item(id: "u", role: "user"), Item(id: "a", role: "assistant"), Item(id: "l", role: "local")]
        XCTAssertEqual(reposition(items).map(\.id), ["u", "l", "a"])
    }

    func testMultipleLocalsInjectedBeforeAssistantKeepingOrder() {
        let items = [
            Item(id: "a", role: "assistant"),
            Item(id: "l1", role: "local"),
            Item(id: "l2", role: "local"),
        ]
        XCTAssertEqual(reposition(items).map(\.id), ["l1", "l2", "a"])
    }

    func testLocalWithTrailingUserStillMovesBeforeAssistant() {
        let items = [Item(id: "a", role: "assistant"), Item(id: "l", role: "local"), Item(id: "u", role: "user")]
        XCTAssertEqual(reposition(items).map(\.id), ["l", "a", "u"])
    }

    func testLocalAnchorsToNearestAssistantAcrossTurns() {
        // l1 距 a2 更近（1）→ 归 a2，保持在 a2 之前
        let items = [
            Item(id: "u1", role: "user"),
            Item(id: "a1", role: "assistant"),
            Item(id: "u2", role: "user"),
            Item(id: "l1", role: "local"),
            Item(id: "a2", role: "assistant"),
        ]
        XCTAssertEqual(reposition(items).map(\.id), ["u1", "a1", "u2", "l1", "a2"])
    }

    func testLocalAfterItsAssistantNotStolenByNextTurn() {
        // l1 距 a1 更近（1）→ 归 a1，而不是被下一轮的 a2 抢走
        let items = [
            Item(id: "u1", role: "user"),
            Item(id: "a1", role: "assistant"),
            Item(id: "l1", role: "local"),
            Item(id: "u2", role: "user"),
            Item(id: "a2", role: "assistant"),
        ]
        XCTAssertEqual(reposition(items).map(\.id), ["u1", "l1", "a1", "u2", "a2"])
    }

    func testEquidistantTieAnchorsToEarlierAssistant() {
        let items = [Item(id: "a1", role: "assistant"), Item(id: "l", role: "local"), Item(id: "a2", role: "assistant")]
        XCTAssertEqual(reposition(items).map(\.id), ["l", "a1", "a2"])
    }

    func testNoAssistantKeepsOrder() {
        let items = [Item(id: "u", role: "user"), Item(id: "l", role: "local")]
        XCTAssertEqual(reposition(items).map(\.id), ["u", "l"])
    }

    func testEmptyAndSingleAreNoop() {
        XCTAssertEqual(reposition([]), [])
        XCTAssertEqual(reposition([Item(id: "l", role: "local")]).map(\.id), ["l"])
        XCTAssertEqual(reposition([Item(id: "a", role: "assistant")]).map(\.id), ["a"])
    }

    func testLocalStaysInItsRoundWhenItsAssistantMissing() {
        // 该轮 assistant 被服务端过滤（无可见正文）时，卡片仍应留在本轮 user 之后，
        // 不能被「最近 assistant」吸到上一轮（用户报的「卡片去上一个消息」）。
        let items = [
            Item(id: "u1", role: "user"),
            Item(id: "a1", role: "assistant"),
            Item(id: "u2", role: "user"),
            Item(id: "l2", role: "local"),
        ]
        XCTAssertEqual(reposition(items).map(\.id), ["u1", "a1", "u2", "l2"])
    }

    func testLocalInCurrentRoundBetweenUserAndAssistant() {
        let items = [
            Item(id: "u1", role: "user"),
            Item(id: "a1", role: "assistant"),
            Item(id: "u2", role: "user"),
            Item(id: "l2", role: "local"),
            Item(id: "a2", role: "assistant"),
        ]
        XCTAssertEqual(reposition(items).map(\.id), ["u1", "a1", "u2", "l2", "a2"])
    }
}
