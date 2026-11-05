import XCTest
@testable import ChatWireCore

final class ToolCallPairingTests: XCTestCase {

    // 回归：pending 到达顺序 [A, C, B]（客户端工具的 tool_call 后到），
    // 结果却按原始调用顺序先到 B —— 必须按 id 命中，而不是吞掉队首 C。
    func testIDMatchWinsOverFIFO() {
        XCTAssertEqual(ToolCallPairing.index(ofResultID: "B", in: ["A", "C", "B"]), 2)
    }

    func testOutOfOrderResultsKeepMatching() {
        var pending: [String?] = ["A", "B", "C"]
        for id in ["B", "A", "C"] {
            guard let idx = ToolCallPairing.index(ofResultID: id, in: pending) else {
                return XCTFail("应命中 \(id)")
            }
            XCTAssertEqual(pending.remove(at: idx), id)
        }
        XCTAssertTrue(pending.isEmpty)
    }

    func testMissingResultIDFallsBackToFIFO() {
        // 旧服务端没有 tool_call_id：保持原来的队首消费行为
        XCTAssertEqual(ToolCallPairing.index(ofResultID: nil, in: ["A", "B"]), 0)
        XCTAssertEqual(ToolCallPairing.index(ofResultID: "", in: ["A", "B"]), 0)
        XCTAssertEqual(ToolCallPairing.index(ofResultID: "   ", in: ["A", "B"]), 0)
    }

    // 已知限制（旧服务端兼容路径）：无 id 的结果只能取队首。
    // 若队首是后到的客户端工具，这条结果就会错配 —— 只能靠服务端升级到带 id 消除。
    func testLegacyFIFOStillMisPAirsWhenHeadIsClientTool() {
        XCTAssertEqual(ToolCallPairing.index(ofResultID: nil, in: ["B(client)", "A(server)"]), 0)
    }

    func testUnknownResultIDDoesNotConsumeOthers() {
        // tool_call 丢了（best-effort 发送）→ 这条结果没有归属，不能错配给下一条
        XCTAssertNil(ToolCallPairing.index(ofResultID: "X", in: ["A", "B"]))
    }

    func testNilPendingIDOnlyMatchesIDLessResults() {
        // pending 里没有 id 的条目无法被带 id 的结果精确命中
        XCTAssertNil(ToolCallPairing.index(ofResultID: "A", in: [nil, "B"]))
        // 无 id 的结果仍按 FIFO 消费队首（即使是 nil 条目）
        XCTAssertEqual(ToolCallPairing.index(ofResultID: nil, in: [nil, "B"]), 0)
    }

    func testDuplicateIDPicksFirstPending() {
        // 理论上 LLM 的调用 id 唯一；真撞了也只消费一条，不会崩
        XCTAssertEqual(ToolCallPairing.index(ofResultID: "A", in: ["A", "A"]), 0)
    }

    func testEmptyPending() {
        XCTAssertNil(ToolCallPairing.index(ofResultID: "A", in: []))
        XCTAssertNil(ToolCallPairing.index(ofResultID: nil, in: []))
    }

    func testWhitespaceInIDsIsIgnored() {
        XCTAssertEqual(ToolCallPairing.index(ofResultID: " call_a ", in: ["call_a"]), 0)
    }

    // MARK: - 事件字段契约（真实服务端 payload fixture）

    // tool_call 的 id 在 `id` 字段（server/internal/tool/registry.go ToolCall）
    func testCallIDFromRealToolCallEvent() {
        let json = #"{"id":"call_00_abc123","tool":"fs","function":"read","args":{"path":"/a"},"display":"读取文件"}"#
        XCTAssertEqual(ToolCallPairing.callID(fromEventJSON: json), "call_00_abc123")
    }

    // tool_result 的 id 在 `tool_call_id` 字段（server/internal/agent toolResultEvent），
    // 与 tool_call 的 `id` 不同名 —— 两边写错一个字母就会静默退化成 FIFO。
    func testResultIDFromRealToolResultEvent() {
        let json = #"{"success":true,"data":{"text":"ok"},"tool_call_id":"call_00_abc123"}"#
        XCTAssertEqual(ToolCallPairing.resultID(fromEventJSON: json), "call_00_abc123")
    }

    // 旧服务端：没有字段 → nil（客户端回退 FIFO，不报错）
    func testResultIDMissingOnLegacyServer() {
        XCTAssertNil(ToolCallPairing.resultID(fromEventJSON: #"{"success":true,"data":{"text":"ok"}}"#))
        XCTAssertNil(ToolCallPairing.resultID(fromEventJSON: #"{"success":false,"error":"boom"}"#))
    }

    func testIDExtractionIgnoresBadPayloads() {
        XCTAssertNil(ToolCallPairing.callID(fromEventJSON: "not json"))
        XCTAssertNil(ToolCallPairing.callID(fromEventJSON: #"{"id":null}"#))
        XCTAssertNil(ToolCallPairing.callID(fromEventJSON: #"{"id":123}"#))
        XCTAssertNil(ToolCallPairing.callID(fromEventJSON: #"{"id":"   "}"#))
        XCTAssertEqual(ToolCallPairing.callID(fromEventJSON: #"{"id":" call_1 "}"#), "call_1")
    }
}
