import XCTest
@testable import SSELineDecoderCore

/// 服务端 `view.WriteSSE` 会把 payload 里的换行拆成多条 `data:` 行；
/// 逐行派发会把 thought_delta / error 这类裸文本在第一个换行处截断。
final class SSEEventAssemblerTests: XCTestCase {

    private func feed(_ lines: [String]) -> (event: String, data: String)? {
        var assembler = SSEEventAssembler()
        var result: (event: String, data: String)?
        for line in lines {
            if let event = assembler.feed(line) {
                XCTAssertNil(result, "一次只喂一个事件")
                result = event
            }
        }
        return result
    }

    func testSingleLineEvent() {
        let evt = feed(["event: message", #"data: {"delta":"hi"}"#, ""])
        XCTAssertEqual(evt?.event, "message")
        XCTAssertEqual(evt?.data, #"{"delta":"hi"}"#)
    }

    // 回归：`data: a` + `data: b` 必须拼成 "a\nb"，不能只派发 a
    func testMultiLineDataIsJoinedWithNewline() {
        let evt = feed(["event: thought_delta", "data: 第一行", "data: 第二行", "data: 第三行", ""])
        XCTAssertEqual(evt?.event, "thought_delta")
        XCTAssertEqual(evt?.data, "第一行\n第二行\n第三行")
    }

    // payload 里的空行（服务端写成 `data: `，trim 后是 `data:`）必须保留
    func testEmptyDataLineIsPreserved() {
        let evt = feed(["data: a", "data:", "data: b", ""])
        XCTAssertEqual(evt?.data, "a\n\nb")
    }

    // 事件名在派发后重置：下一个没有 event: 的事件不该继承上一个名字
    func testEventNameResetsAfterDispatch() {
        var assembler = SSEEventAssembler()
        XCTAssertNil(assembler.feed("event: status"))
        XCTAssertNil(assembler.feed("data: x"))
        XCTAssertEqual(assembler.feed("")?.event, "status")
        XCTAssertNil(assembler.feed("data: y"))
        XCTAssertEqual(assembler.feed("")?.event, "")
    }

    func testCommentAndUnknownFieldsIgnored() {
        let evt = feed([": keep-alive", "id: 42", "retry: 1000", "event: done", "data: {}", ""])
        XCTAssertEqual(evt?.event, "done")
        XCTAssertEqual(evt?.data, "{}")
    }

    func testBlankLineWithoutDataDoesNotDispatch() {
        var assembler = SSEEventAssembler()
        XCTAssertNil(assembler.feed(""))
        XCTAssertNil(assembler.feed("event: message"))
        XCTAssertNil(assembler.feed(""))
    }

    // 断流重连：半截事件必须丢掉，不能拼进下一条流
    func testResetDropsPartialEvent() {
        var assembler = SSEEventAssembler()
        XCTAssertNil(assembler.feed("event: message"))
        XCTAssertNil(assembler.feed("data: half"))
        assembler.reset()
        XCTAssertNil(assembler.feed(""))
        XCTAssertNil(assembler.feed("data: fresh"))
        XCTAssertEqual(assembler.feed("")?.data, "fresh")
    }
}
