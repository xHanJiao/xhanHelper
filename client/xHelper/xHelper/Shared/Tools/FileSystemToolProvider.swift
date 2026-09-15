#if os(macOS)
import Foundation
import Dispatch
import Darwin

/// 进程抓取结果（供 grep/glob 判定是否预算截断/超时）。
nonisolated struct ProcessCaptureResult: Sendable {
    let capped: Bool
    let timedOut: Bool
    let stderrTail: String
}

/// 线程安全的布尔标志（看门狗 ↔ 读取循环）。
nonisolated private final class CaptureFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false
    func set() { lock.withLock { value = true } }
    var isSet: Bool { lock.withLock { value } }
}

/// fs 工具诊断日志：nonisolated 跳主线程写入 LogBuffer（诊断页可查），镜像 MCPLog 模式。
nonisolated func fsToolLog(_ message: String) {
    Task { @MainActor in LogBuffer.shared.diag(message) }
}

/// 后台排空子进程 stderr 并保留末尾片段（诊断用），避免 64KB 管道缓冲写满导致子进程阻塞。
nonisolated private final class StderrTail: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()
    private let cap = 4096

    func drain(_ fh: FileHandle) {
        while true {
            let chunk = fh.readData(ofLength: 4096)
            if chunk.isEmpty { break }
            lock.withLock {
                data.append(chunk)
                if data.count > cap { data.removeFirst(data.count - cap) }
            }
        }
    }

    var tail: String { lock.withLock { String(data: data, encoding: .utf8) ?? "" } }
}

final class FileSystemToolProvider: @unchecked Sendable {
    let namespace = "fs"

    let maxResults = 100
    let maxReadBytes = 50 * 1024
    let maxLineChars = 2000
    let defaultReadLimit = 2000

    /// grep/glob 子进程的客户端硬超时（必须小于服务端 client_tool_timeout_secs=120）。
    static let processTimeout: TimeInterval = 30

    /// 收集循环的纯安全网预算（grep/glob 的 walk 停止由"第 maxResults+1 条"决定，
    /// 预算只防失控输出撑爆内存；正常流程永不触发，保证结果确定性）。
    let collectionSafetyBudget = 8 * 1024 * 1024

    /// 解码全量文件（GB18030 等非 UTF-8 编码）的大小上限。
    let maxDecodeBytes = 5 * 1024 * 1024

    /// 单次进程调用的有效超时：取客户端下发截止时间与本地硬上限的较小值。
    func effectiveProcessTimeout(_ ctx: ExecuteContext) -> TimeInterval {
        min(ctx.deadlineSecs ?? Self.processTimeout, Self.processTimeout)
    }

    /// 会话级"已读"追踪：path(real) → mtime。
    /// write/edit 覆盖已有文件前必须 read 过（防盲写），mtime 变化视为外部改动未读。
    private let readLock = NSLock()
    private var readPaths: [String: Date] = [:]

    func markRead(_ path: String) {
        let mtime = (try? FileManager.default.attributesOfItem(atPath: path)[.modificationDate]) as? Date
        readLock.lock()
        readPaths[path] = mtime ?? Date()
        readLock.unlock()
    }

    func hasRead(_ path: String) -> Bool {
        readLock.lock()
        defer { readLock.unlock() }
        guard let stored = readPaths[path] else { return false }
        let mtime = (try? FileManager.default.attributesOfItem(atPath: path)[.modificationDate]) as? Date
        return stored == mtime
    }

    /// 解析路径：展开 ~、标准化（.. / 重复斜杠），并解析 symlink 到真实路径。
    /// 存在性由调用方确认（不存在时保持标准化路径，供错误提示使用）。
    static func resolvePath(_ path: String) -> String {
        var resolved = (path as NSString).expandingTildeInPath
        resolved = (resolved as NSString).standardizingPath
        let url = URL(fileURLWithPath: resolved).resolvingSymlinksInPath()
        let real = url.path
        if FileManager.default.fileExists(atPath: real) {
            return real
        }
        return resolved
    }

    /// 输出预算上限：client 下发预算（20KB）− 256B 余量（JSON 信封）；
    /// 无预算时用 50KB 绝对上限。
    func budgetCap(_ budgetBytes: Int?) -> Int {
        let budget = budgetBytes ?? maxReadBytes
        return min(max(budget - 256, 1024), maxReadBytes)
    }

    /// 结果超预算时截断并追加提示。KB 显示四舍五入（与下发预算口径一致，如 20KB）。
    func cappedOutput(_ output: String, budget: Int, continueHint: String) -> (text: String, truncated: Bool) {
        if output.utf8.count <= budget {
            return (output, false)
        }
        var text = String(output.prefix(budget))
        if let range = text.range(of: "\n", options: .backwards) {
            text = String(text[..<range.lowerBound])
        }
        return (text + "\n(Output capped at \((budget + 1023) / 1024) KB. \(continueHint))", true)
    }

    /// 流式读取进程输出：按行回调，onLine 返回 false 即 terminate（收集器收满时提前停，
    /// 不再靠预算硬掐——预算改为纯安全网）。超预算 terminate 并返回 capped=true。
    /// 行缓冲处理 chunk 边界断行；数据只累积到预算即截断，避免大输出死锁。
    ///
    /// 超时看门狗：到 `timeout` 秒仍运行 → terminate()，宽限 2s 后 SIGKILL 兜底。
    /// stderr 由后台任务排空（保留末尾供诊断），避免 64KB 管道缓冲写满导致子进程阻塞。
    nonisolated static func drainToEOF(_ fh: FileHandle) {
        while true {
            if fh.readData(ofLength: 8192).isEmpty { break }
        }
    }

    func runCapturedProcessLines(
        _ proc: Process,
        budget: Int,
        timeout: TimeInterval = FileSystemToolProvider.processTimeout,
        onLine: @escaping (String) -> Bool
    ) async -> ProcessCaptureResult {
        await withCheckedContinuation { (continuation: CheckedContinuation<ProcessCaptureResult, Never>) in
            Task.detached(priority: .userInitiated) {
                let pipe = Pipe()
                let errPipe = Pipe()
                proc.standardOutput = pipe
                proc.standardError = errPipe
                let fh = pipe.fileHandleForReading

                let errTail = StderrTail()
                let errDrain = Task.detached(priority: .utility) {
                    errTail.drain(errPipe.fileHandleForReading)
                }

                do {
                    try proc.run()
                } catch {
                    try? fh.close()
                    errDrain.cancel()
                    continuation.resume(returning: ProcessCaptureResult(capped: false, timedOut: false, stderrTail: errTail.tail))
                    return
                }

                // 看门狗：到点 terminate；宽限 2s 仍在跑则 SIGKILL（防忽略 SIGTERM）
                let timedOutFlag = CaptureFlag()
                let watchdog = DispatchSource.makeTimerSource(queue: DispatchQueue.global(qos: .utility))
                watchdog.schedule(deadline: .now() + timeout)
                watchdog.setEventHandler {
                    if !proc.isRunning { return }
                    timedOutFlag.set()
                    proc.terminate()
                    DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 2) {
                        if proc.isRunning { kill(proc.processIdentifier, SIGKILL) }
                    }
                }
                watchdog.resume()
                defer { watchdog.cancel() }

                var data = Data()
                var lineBuffer = ""
                var capped = false
                while true {
                    let chunk = fh.readData(ofLength: 8192)
                    if chunk.isEmpty { break }
                    data.append(chunk)
                    // 先处理行（收集器可能在收满时提前停）——让 match 上限优先于预算掐断，
                    // 保证翻页语义稳定（~100 条/页），预算只作安全网
                    lineBuffer += String(data: chunk, encoding: .utf8) ?? ""
                    var stop = false
                    while let newline = lineBuffer.firstIndex(of: "\n") {
                        let line = String(lineBuffer[..<newline])
                        lineBuffer.removeSubrange(...newline)
                        if !onLine(line) { stop = true; break }
                    }
                    if stop {
                        proc.terminate()
                        FileSystemToolProvider.drainToEOF(fh)
                        break
                    }
                    if data.count > budget {
                        capped = true
                        proc.terminate()
                        FileSystemToolProvider.drainToEOF(fh)
                        break
                    }
                }
                proc.waitUntilExit()
                try? fh.close()
                errDrain.cancel()
                continuation.resume(returning: ProcessCaptureResult(
                    capped: capped, timedOut: timedOutFlag.isSet, stderrTail: errTail.tail))
            }
        }
    }
}

extension FileSystemToolProvider: LocalToolProvider {

    func toolSchemas() -> [LocalToolSchema] {
        [
            LocalToolSchema(
                name: "glob",
                description: """
                快速文件模式匹配工具。
                - 支持 glob 模式如 "**/*.go" 或 "src/**/*.ts"
                - 返回匹配的文件列表，每行 "绝对路径\\t字节数"，最多 100 条
                - 自动尊重 .gitignore
                - 当你需要按文件名模式查找文件时使用本工具
                - 你不确定文件确切路径时，先用 glob 找到文件，再用 read 读取
                - 你可以同时调用多个工具，批量 speculative 搜索通常更高效
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "pattern": [
                            "type": "string",
                            "description": "Glob 模式，如 **/*.go 或 src/**/*.ts",
                        ],
                        "path": [
                            "type": "string",
                            "description": "搜索起始目录，必须为绝对路径",
                        ],
                    ],
                    "required": ["pattern", "path"],
                ],
                isReadOnly: true
            ),
            LocalToolSchema(
                name: "grep",
                description: """
                文件内容正则搜索工具。
                - 搜索文件内容，支持完整正则语法（如 "log.*Error"、"func\\s+\\w+"）
                - 通过 include 参数过滤文件类型（如 "*.go"、"*.{ts,tsx}"）
                - 返回文件路径和行号，附带匹配行内容，每页最多 100 条
                - 结果末尾提示 "Use skip=N to continue" 时，用 skip 参数翻下一页（从第 N 条开始）
                - 敏感文件（.env、.git 内部等）的匹配会自动省略，省略数会标注
                - 当你需要在文件中定位特定内容时使用本工具——先用 grep 找到位置，再用 read 读上下文
                - 对于大文件或长行的文件，用 grep 定位比直接 read 更高效
                - 对 PDF（有文本层）按页提取文本做正则搜索，命中返回 Page N, line M（页内行号）；再用 read(filePath, page_offset=N) 读上下文。扫描件（无文本层）无法搜索。
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "pattern": [
                            "type": "string",
                            "description": "正则表达式，如 log.*Error 或 func\\s+\\w+",
                        ],
                        "path": [
                            "type": "string",
                            "description": "搜索起始目录，必须为绝对路径",
                        ],
                        "include": [
                            "type": "string",
                            "description": "文件类型过滤，如 *.go 或 *.{ts,tsx}。可选。",
                        ],
                        "skip": [
                            "type": "integer",
                            "description": "从第 N 条匹配开始返回（翻页用，默认 0）",
                        ],
                    ],
                    "required": ["pattern", "path"],
                ],
                isReadOnly: true
            ),
            LocalToolSchema(
                name: "write",
                description: """
                写入本地文件（新建 / 覆盖 / 追加）。

                用法：
                - path 必须是绝对路径；content 为要写入的完整内容。
                - mode=overwrite（默认）：覆盖整个文件；mode=append：追加到文件末尾。
                - 覆盖或追加已有文件前，必须先在本会话内用 read 读过该文件，否则报错。
                - 修改已有文件优先用 edit 渐进修改，避免整文件重写（write 用于新建或整体替换）。
                - 每次只写一个小片段，多次调用渐进完成，不要一次写入超大内容。
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "path": [
                            "type": "string",
                            "description": "文件绝对路径",
                        ],
                        "content": [
                            "type": "string",
                            "description": "要写入的完整内容",
                        ],
                        "mode": [
                            "type": "string",
                            "enum": ["overwrite", "append"],
                            "description": "overwrite=覆盖（默认）/ append=追加",
                        ],
                    ],
                    "required": ["path", "content"],
                ],
                isReadOnly: false
            ),
            LocalToolSchema(
                name: "edit",
                description: """
                文件内精确字符串替换（渐进修改已有文件的主力工具）。

                用法：
                - path 必须是绝对路径；old_string 为要替换的原文，new_string 为替换后的内容。
                - 修改已有文件优先用本工具，每次只改一个小片段，多次调用渐进完成。
                - 编辑前必须先用 read 读过该文件（本会话内），否则报错。
                - old_string 必须在文件中唯一匹配（含空格/缩进/换行），否则报错。
                - 多匹配时报错并提示：加大上下文使其唯一，或 replace_all=true 全部替换。
                - old_string 为空拒绝；old_string 与 new_string 相同拒绝。
                - 文件行尾风格（LF/CRLF）自动保留；新建或整体替换用 write。
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "path": [
                            "type": "string",
                            "description": "文件绝对路径",
                        ],
                        "old_string": [
                            "type": "string",
                            "description": "要替换的原文（必须与文件内容精确一致，含空白/缩进）",
                        ],
                        "new_string": [
                            "type": "string",
                            "description": "替换后的内容（必须与 old_string 不同）",
                        ],
                        "replace_all": [
                            "type": "boolean",
                            "description": "替换所有匹配（默认 false：要求唯一匹配）",
                        ],
                    ],
                    "required": ["path", "old_string", "new_string"],
                ],
                isReadOnly: false
            ),
            LocalToolSchema(
                name: "read",
                description: """
                读取本地文件系统中的文件或目录。路径不存在时返回错误。

                用法：
                - filePath 必须是绝对路径。
                - 默认从文件开头返回最多 2000 行。
                - offset 参数是起始行号（1-indexed）。
                - 读取后续内容时，用更大的 offset 再次调用。
                - 需要在大文件或长行文件中定位内容时，先用 grep 找到位置。
                - 不确定文件路径时，先用 glob 查找。
                - 文件内容返回格式：每行前加行号，如 "1: package main\\n2:\\n3: import (\\n"
                - 目录返回格式：每行一条条目（无行号），目录名末尾带 /
                - 单行超过 2000 字符会被截断。
                - 长行文件（如压缩 JSON、单行 CSV）改用 char_offset/char_limit 按字符窗口读取。
                - 结果超过注入预算时会自动落盘并给出 output_path（truncated=true）：改用 fs__grep 定位该副本，
                  或 fs__read(output_path, offset/char_offset) 续读，不要重复整读原文件。
                - 可以同时读取多个文件（并行调用）。
                - 避免小块切割读取（30 行一读）；需要更多上下文时就一次读更大的范围。
                - HTML 文件（.html/.htm）会剥离 <script>/<style> 内容（替换为占位标记），行号保持不变。
                - 非 UTF-8 编码（GBK/GB18030/UTF-16）自动解码。
                - 图片（JPEG/PNG/GIF/WebP）以 base64 返回。
                - PDF 提取纯文本（按页），page_offset/page_limit 分页续读；页内被截断时按 page_char_offset（页内字符偏移）继续；扫描件（无文本层）无法提取。
                - PDF 页眉/页脚与页码会自动清洗，输出为纯页文本（不注入页码标记）；raw=true 可返回未清洗的原始文本（诊断用）。
                - 页码基准：PDF 物理页、从 1 起、含封面；打印页码可能与之不同。
                - 注意：PDF 表格按文本流提取，行列关系会丢失、跨行数字可能被切断，表格数值不可直接解析；财务数据请用版面感知工具或 XBRL。
                - 文件末尾会标注字节大小（size）。
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "filePath": [
                            "type": "string",
                            "description": "文件或目录的绝对路径",
                        ],
                        "offset": [
                            "type": "integer",
                            "description": "起始行号（1-indexed），默认 1",
                        ],
                        "limit": [
                            "type": "integer",
                            "description": "最大行数，默认 2000",
                        ],
                        "char_offset": [
                            "type": "integer",
                            "description": "字符偏移（0-indexed，长行/无换行文件按字符定位；提供后忽略 offset/limit，按字符窗口返回）",
                        ],
                        "char_limit": [
                            "type": "integer",
                            "description": "char_offset 模式的字符数上限，默认 4000",
                        ],
                        "page_offset": [
                            "type": "integer",
                            "description": "PDF 起始页（1-indexed），默认 1",
                        ],
                        "page_limit": [
                            "type": "integer",
                            "description": "PDF 最大页数，默认 10",
                        ],
                        "page_char_offset": [
                            "type": "integer",
                            "description": "PDF 起始页内的字符偏移（仅作用于起始页，续读用，默认 0）",
                        ],
                        "raw": [
                            "type": "boolean",
                            "description": "诊断用：返回未清洗的原始 PDF 文本（含页眉/页脚/页码），默认 false",
                        ],
                    ],
                    "required": ["filePath"],
                ],
                isReadOnly: true
            ),
        ]
    }

    static func extractPaths(flatName: String, arguments: [String: Any]) -> [String] {
        var paths: [String] = []
        if let p = arguments["path"] as? String, !p.isEmpty {
            paths.append(p)
        }
        if let fp = arguments["filePath"] as? String, !fp.isEmpty {
            paths.append(fp)
        }
        return paths.map { ($0 as NSString).expandingTildeInPath }
    }

    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        switch tool {
        case "glob":
            return try await handleGlob(arguments: arguments, context: context)
        case "grep":
            return try await handleGrep(arguments: arguments, context: context)
        case "write":
            return try await handleWrite(arguments: arguments, context: context)
        case "edit":
            return try await handleEdit(arguments: arguments, context: context)
        case "read":
            return try await handleRead(arguments: arguments, context: context)
        default:
            return LocalToolResult(serverResult: nil, serverError: "未知函数: \(tool)", clientAction: nil)
        }
    }
}
#endif
