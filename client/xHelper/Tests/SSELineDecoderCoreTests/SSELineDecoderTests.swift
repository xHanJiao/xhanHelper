import XCTest
@testable import SSELineDecoderCore

final class SSELineDecoderTests: XCTestCase {

    /// 网络块会在任意字节处切开（包括多字节 UTF-8 字符中间）。
    /// 逐字节喂入必须和一次性喂入产出完全相同的行序列。
    func testSplitAtEveryByteBoundaryKeepsAllLines() {
        let stream = "event: subagent\n"
            + "data: {\"msg\":\"科目三🙂\"}\n"
            + "\n"
            + "event: tool_result\n"
            + "data: {\"path\":\"/Users/你/Desktop/模拟器.html\"}\n"
            + "\n"
        let expected = [
            "event: subagent",
            "data: {\"msg\":\"科目三🙂\"}",
            "",
            "event: tool_result",
            "data: {\"path\":\"/Users/你/Desktop/模拟器.html\"}",
            "",
        ]

        var decoder = SSELineDecoder()
        var lines: [String] = []
        var sawInvalid = false
        for byte in Array(stream.utf8) {
            let out = decoder.append(Data([byte]))
            lines.append(contentsOf: out.lines)
            sawInvalid = sawInvalid || out.hadInvalidUTF8
        }
        XCTAssertEqual(lines, expected)
        XCTAssertFalse(sawInvalid, "合法 UTF-8 被切成任意块都不应报告坏字节")
    }

    func testChunkSizesAreIrrelevant() {
        let stream = "event: status\ndata: 正在等待客户端执行 fs.grep…\n\n"
        let expected = ["event: status", "data: 正在等待客户端执行 fs.grep…", ""]
        for size in stride(from: 1, through: 16, by: 1) {
            var decoder = SSELineDecoder()
            var lines: [String] = []
            var rest = Array(stream.utf8)
            while !rest.isEmpty {
                let take = min(size, rest.count)
                let chunk = Data(rest.prefix(take))
                rest.removeFirst(take)
                lines.append(contentsOf: decoder.append(chunk).lines)
            }
            XCTAssertEqual(lines, expected, "chunk size \(size)")
        }
    }

    /// 真的坏字节：只替换成 U+FFFD，不丢整行（旧实现会丢掉整块）。
    func testInvalidUTF8IsReplacedNotDropped() {
        let bytes = Data([0x64, 0x61, 0x74, 0x61, 0x3A, 0x20, 0xFF, 0x0A]) // "data: " + 0xFF + "\n"
        var decoder = SSELineDecoder()
        let out = decoder.append(bytes)
        XCTAssertTrue(out.hadInvalidUTF8)
        XCTAssertEqual(out.lines.count, 1)
        XCTAssertEqual(out.lines.first, "data: \u{FFFD}")
    }

    func testIncompleteLineIsBufferedUntilNewline() {
        var decoder = SSELineDecoder()
        XCTAssertTrue(decoder.append(Data("even".utf8)).lines.isEmpty)
        XCTAssertTrue(decoder.append(Data("t: x".utf8)).lines.isEmpty)
        XCTAssertEqual(decoder.append(Data("\n".utf8)).lines, ["event: x"])
    }

    /// 分工：解码器只认 `\n`，`\r` 留给调用方自己 trim（SSEClient 会 trim；fs 抓取沿原行为不 trim）。
    /// 钉住这点，防止以后有人以为解码器已经处理了换行而删掉调用方的 trim。
    func testCarriageReturnIsLeftToCaller() {
        var decoder = SSELineDecoder()
        let out = decoder.append(Data("event: x\r\ndata: y\r\n".utf8))
        XCTAssertEqual(out.lines, ["event: x\r", "data: y\r"])
    }

    func testResetDropsPartialLine() {
        var decoder = SSELineDecoder()
        _ = decoder.append(Data("data: half".utf8))
        decoder.reset()
        XCTAssertEqual(decoder.append(Data("next line\n".utf8)).lines, ["next line"])
    }
}
