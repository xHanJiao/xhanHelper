import XCTest
@testable import ChatWireCore

/// SSE `event:` 名与客户端类型的映射是两端契约：
/// 服务端 `controller/chat.go` 转发什么，这里就得认什么；漏一个就是静默丢事件。
final class StreamEventMappingTests: XCTestCase {

    // 与 server/internal/controller/chat.go 的转发 switch 一一对应。
    func testAllServerForwardedEventsMap() {
        let expected: [String: StreamEventType] = [
            "text_delta": .message,
            "message": .message,
            "plan": .plan,
            "plan_node_start": .planNodeStart,
            "plan_node_done": .planNodeDone,
            "plan_done": .planDone,
            "thought": .thought,
            "thought_delta": .thoughtDelta,
            "thought_done": .thoughtDone,
            "status": .status,
            "verify": .verify,
            "tool_call": .toolCall,
            "tool_result": .toolResult,
            "client_tool_call": .clientToolCall,
            "subagent": .subagent,
            "reminders_changed": .remindersChanged,
            "meta": .meta,
            "idle": .idle,
            "reset": .reset,
            "done": .done,
            "error": .error,
        ]
        for (name, type) in expected {
            XCTAssertEqual(StreamEventType.from(eventName: name), type, "事件名未映射：\(name)")
        }
    }

    // 服务端新增事件而客户端没跟上 → nil（调用方记一次 warning），不能猜成别的事件
    func testUnknownEventIsNil() {
        XCTAssertNil(StreamEventType.from(eventName: "plan_progress"))
        XCTAssertNil(StreamEventType.from(eventName: ""))
        XCTAssertNil(StreamEventType.from(eventName: "DONE"))
    }
}
