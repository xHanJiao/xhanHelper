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
            isAssistant: { $0.role == "assistant" }
        )
    }

    func testLocalMovedAfterNextAssistant() {
        let items = [Item(id: "u", role: "user"), Item(id: "l", role: "local"), Item(id: "a", role: "assistant")]
        XCTAssertEqual(reposition(items).map(\.id), ["u", "a", "l"])
    }

    func testMultipleLocalsAfterSameAssistant() {
        let items = [
            Item(id: "a", role: "assistant"),
            Item(id: "l1", role: "local"),
            Item(id: "l2", role: "local"),
        ]
        XCTAssertEqual(reposition(items).map(\.id), ["a", "l1", "l2"])
    }

    func testLocalWithNoFollowingAssistantStays() {
        let items = [Item(id: "a", role: "assistant"), Item(id: "u", role: "user"), Item(id: "l", role: "local")]
        XCTAssertEqual(reposition(items).map(\.id), ["a", "u", "l"])
    }

    func testAlreadyCorrectOrderUnchanged() {
        let items = [Item(id: "a", role: "assistant"), Item(id: "l", role: "local"), Item(id: "u", role: "user")]
        XCTAssertEqual(reposition(items).map(\.id), ["a", "l", "u"])
    }

    func testInterleavedMovesToNearestFollowingAssistant() {
        let items = [
            Item(id: "u1", role: "user"),
            Item(id: "a1", role: "assistant"),
            Item(id: "u2", role: "user"),
            Item(id: "l1", role: "local"),
            Item(id: "a2", role: "assistant"),
        ]
        XCTAssertEqual(reposition(items).map(\.id), ["u1", "a1", "u2", "a2", "l1"])
    }

    func testEmptyAndSingleAreNoop() {
        XCTAssertEqual(reposition([]), [])
        XCTAssertEqual(reposition([Item(id: "l", role: "local")]).map(\.id), ["l"])
        XCTAssertEqual(reposition([Item(id: "a", role: "assistant")]).map(\.id), ["a"])
    }
}
