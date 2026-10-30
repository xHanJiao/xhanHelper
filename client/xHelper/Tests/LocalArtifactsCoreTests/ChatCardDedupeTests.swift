import XCTest
@testable import LocalArtifactsCore

final class ChatCardDedupeTests: XCTestCase {
    func testLastOwnerKeepsLatestMessage() {
        // 同一页在 4 条消息里都挂了卡：改版是原地覆盖，只有最新那条该显示。
        let owner = ChatCardDedupe.lastOwner([
            (cardID: "html:page-1", messageID: "m1"),
            (cardID: "html:page-1", messageID: "m2"),
            (cardID: "html:page-1", messageID: "m3"),
            (cardID: "html:page-1", messageID: "m4"),
        ])
        XCTAssertEqual(owner["html:page-1"], "m4")
    }

    func testDifferentCardsKeepOwnMessages() {
        let owner = ChatCardDedupe.lastOwner([
            (cardID: "html:page-1", messageID: "m1"),
            (cardID: "doc:doc-1", messageID: "m2"),
        ])
        XCTAssertEqual(owner["html:page-1"], "m1")
        XCTAssertEqual(owner["doc:doc-1"], "m2")
    }
}
