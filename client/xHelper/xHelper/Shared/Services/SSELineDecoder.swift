import Foundation

/// 把任意切分的字节流按 `\n` 切成完整行（SSE、子进程输出等任何按行分隔的流通用）。
/// 不负责去 `\r`：调用方自己 trim。
///
/// 为什么不能逐块 `String(data:encoding:.utf8)`：URLSession 的 data 块边界是任意的，
/// 会切在中文字符/emoji 的字节中间。逐块解码时 nil → 整块被丢，`event:`/`data:` 行
/// 因此缺口，后续事件被静默吞掉（实测丢过 client_tool_call，服务端白等 240s）。
/// 这里先按字节缓冲到完整一行再解码；行内真有非法字节时只替换坏字节，不丢整行。
/// 必须 nonisolated：两个使用点（URLSession delegate 队列、Task.detached）都不在主线程，
/// 项目默认按 MainActor 推断（见 docs/design/mac-apps/swift6-concurrency-fix.md）。
nonisolated struct SSELineDecoder {
    private var buffer = Data()

    /// 追加一块网络数据，返回本次凑齐的完整行。
    /// `hadInvalidUTF8` = 本次有行含非法 UTF-8 字节（已被替换，调用方负责记日志）。
    mutating func append(_ data: Data) -> (lines: [String], hadInvalidUTF8: Bool) {
        buffer.append(data)
        var lines: [String] = []
        var hadInvalid = false
        while let newline = buffer.firstIndex(of: 0x0A) {
            let bytes = buffer[buffer.startIndex..<newline]
            let decoded = Self.decodeLine(bytes)
            if decoded.hadInvalidUTF8 { hadInvalid = true }
            lines.append(decoded.text)
            buffer.removeSubrange(buffer.startIndex...newline)
        }
        return (lines, hadInvalid)
    }

    mutating func reset() {
        buffer.removeAll(keepingCapacity: true)
    }

    /// 完整行解码。非法 UTF-8 用 U+FFFD 替换（`String(decoding:as:)`），不抛不丢。
    static func decodeLine(_ bytes: Data.SubSequence) -> (text: String, hadInvalidUTF8: Bool) {
        let data = Data(bytes)
        if let text = String(data: data, encoding: .utf8) {
            return (text, false)
        }
        return (String(decoding: data, as: UTF8.self), true)
    }
}

/// 按 SSE 规范把「行」拼成「事件」：`data:` 允许多行，遇到空行才派发（多行用 `\n` 拼回）。
///
/// 服务端 `view.WriteSSE` 会把 payload 里的换行拆成多条 `data:` 行。逐行派发会把
/// `thought_delta` / `error` 这类裸文本在第一个换行处截断，后面的行还会被当成未知事件丢掉。
nonisolated struct SSEEventAssembler {
    private var event = ""
    private var dataLines: [String] = []
    /// 最后一个事件 ID（`id:`），按规范跨事件保留；续传用它当断点。
    private var lastID: String?

    /// 喂一行（调用方已做 trim），凑齐一个事件时返回 `(event, data, id)`。
    mutating func feed(_ line: String) -> (event: String, data: String, id: String?)? {
        if line.hasPrefix("event:") {
            event = Self.payload(of: line, prefixLength: 6)
            return nil
        }
        if line.hasPrefix("data:") {
            dataLines.append(Self.payload(of: line, prefixLength: 5))
            return nil
        }
        if line.hasPrefix("id:") {
            lastID = Self.payload(of: line, prefixLength: 3)
            return nil
        }
        guard line.isEmpty else { return nil } // 注释（`: ping`）与未知字段一律忽略
        defer { reset() }
        guard !dataLines.isEmpty else { return nil }
        return (event, dataLines.joined(separator: "\n"), lastID)
    }

    mutating func reset() {
        event = ""
        dataLines.removeAll(keepingCapacity: true)
    }

    /// 取字段值并去掉冒号后的一个空格（`data: x` / `data:x` 都认；空行为空串）。
    private static func payload(of line: String, prefixLength: Int) -> String {
        var value = String(line.dropFirst(prefixLength))
        if value.hasPrefix(" ") { value.removeFirst() }
        return value
    }
}
