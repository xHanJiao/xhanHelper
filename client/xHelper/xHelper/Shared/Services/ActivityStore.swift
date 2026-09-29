import Foundation
import OSLog

/// 客户端事件的本地落盘队列（设计篇 5.2）：事件逐条追加到 JSONL，按段成文件，搬完整段删。
///
/// - 目录落 Application Support（`LocalPaths.dataRoot/activity`），**不落 Caches**（系统会清理）。
/// - 逐条即时追加、不做内存缓冲：量级 ~2000 条/天，缓冲省不下什么却会把崩溃窗口内的事件丢掉。
/// - 当前段 `current.jsonl`；达到 `maxSegmentEvents` 或上传节拍时封为 `seg-<毫秒>-<短id>.jsonl`。
/// - 坏行跳过并记日志（不让一行脏数据毁掉整段）；写盘失败不重试，记进独立失败日志（设计篇 4.5）。
actor ActivityStore {
    static let maxSegmentEvents = 500
    /// 段字节上限（对齐服务端 1 MiB）；超限段会被服务端 400 隔离而永久丢失，故提前切段。
    static let maxSegmentBytes = 1 << 20

    private let dir: URL
    private let fm = FileManager.default
    private let log = Logger(subsystem: "com.xhan.xhanHelper", category: "activity")
    private var currentCount: Int
    private var currentBytes: Int

    init(directory: URL) {
        self.dir = directory
        do {
            try fm.createDirectory(at: directory, withIntermediateDirectories: true)
        } catch {
            // 目录建不出来 = 后续写盘必失败，这里只记一次；真正失败由 append 的 noteFailure 记。
            log.error("activity: 建目录失败 \(directory.path): \(error)")
        }
        let cur = directory.appendingPathComponent("current.jsonl")
        if let size = (try? FileManager.default.attributesOfItem(atPath: cur.path))?[.size] as? Int {
            self.currentBytes = size
        } else {
            self.currentBytes = 0
        }
        if let text = try? String(contentsOf: cur, encoding: .utf8) {
            self.currentCount = text.split(separator: "\n", omittingEmptySubsequences: true).count
        } else {
            // 文件不存在=正常（0）；存在却读不了 → 记一次，别静默把计数当 0。
            if FileManager.default.fileExists(atPath: cur.path) {
                log.error("activity: 读当前段计数失败，从 0 起 \(cur.path)")
            }
            self.currentCount = 0
        }
    }

    private var currentURL: URL { dir.appendingPathComponent("current.jsonl") }

    /// 追加若干事件到当前段。写失败抛错，调用方记失败日志并继续（fire-and-forget，不重试）。
    func append(_ events: [ActivityEvent]) throws {
        guard !events.isEmpty else { return }
        let lines = try events.map { try Self.encodeLine($0) }.joined(separator: "\n") + "\n"
        let data = Data(lines.utf8)
        if fm.fileExists(atPath: currentURL.path) {
            // 已存在只能追加：打不开就抛，绝不覆盖已有段（.atomic 会整文件替换）。
            // forUpdating（读写）而非 forWritingTo：需要读末字节判半行，写句柄读不了。
            let handle = try FileHandle(forUpdating: currentURL)
            defer { try? handle.close() }
            let end = try handle.seekToEnd()
            if end > 0, try !Self.endsWithNewline(handle, end: end) {
                // 上次进程写盘中途被杀留下的半行：补一个换行，避免它与本条拼成一行（会一起变坏行丢掉）。
                try handle.write(contentsOf: Data([0x0A]))
            }
            try handle.write(contentsOf: data)
        } else {
            try data.write(to: currentURL, options: .atomic)
        }
        currentCount += events.count
        currentBytes += data.count
        if currentCount >= Self.maxSegmentEvents || currentBytes >= Self.maxSegmentBytes {
            _ = try sealCurrent()
        }
    }

    /// 把当前段封成不可变段文件（供上传）。空则返回 nil。
    @discardableResult
    func sealCurrent() throws -> URL? {
        guard currentCount > 0, fm.fileExists(atPath: currentURL.path) else { return nil }
        let name = "seg-\(Self.millis())\(UUID().uuidString.prefix(8)).jsonl"
        let dst = dir.appendingPathComponent(name)
        try fm.moveItem(at: currentURL, to: dst)
        currentCount = 0
        currentBytes = 0
        return dst
    }

    /// 已封待传的段，按文件名升序（文件名带毫秒时间戳 → 天然时序）。
    func pendingSegments() throws -> [URL] {
        let files = try fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil, options: .skipsHiddenFiles)
        return files
            .filter { $0.lastPathComponent.hasPrefix("seg-") && $0.pathExtension == "jsonl" }
            .sorted { $0.lastPathComponent < $1.lastPathComponent }
    }

    /// 读整段事件；坏行跳过并记日志。
    func load(_ url: URL) throws -> [ActivityEvent] {
        let text = try String(contentsOf: url, encoding: .utf8)
        var out: [ActivityEvent] = []
        for line in text.split(separator: "\n", omittingEmptySubsequences: true) {
            do {
                out.append(try Self.decodeEvent(Data(line.utf8)))
            } catch {
                log.error("activity: 跳过坏行 \(url.lastPathComponent): \(error)")
            }
        }
        return out
    }

    /// 上传确认后删段（一段一请求，ack 即代表这段全部落库，无需比对水位）。
    func remove(_ url: URL) {
        do {
            try fm.removeItem(at: url)
        } catch {
            // 删不掉 → 段会被反复重投（服务端幂等无副作用），但必须留痕。
            log.error("activity: 删段失败 \(url.lastPathComponent): \(error)")
        }
    }

    /// 段本身不合法（4xx）：改名 .bad 隔离，避免毒丸段每轮重投、卡住后面的段。
    func quarantine(_ url: URL) {
        let dst = url.appendingPathExtension("bad")
        do {
            try fm.moveItem(at: url, to: dst)
        } catch {
            log.error("activity: 隔离失败 \(url.lastPathComponent): \(error)")
        }
    }

    /// 独立失败日志（不混主日志，设计篇 4.5）：每行一条「时间 + 原因」，失败次数/最后时间由此可数。
    func noteFailure(_ message: String) {
        let line = "\(ActivityIdentity.isoMillis())\t\(message)\n"
        guard let data = line.data(using: .utf8) else { return }
        let url = dir.appendingPathComponent("failures.log")
        do {
            if fm.fileExists(atPath: url.path) {
                let handle = try FileHandle(forWritingTo: url)
                defer { try? handle.close() }
                _ = try handle.seekToEnd()
                try handle.write(contentsOf: data)
            } else {
                try data.write(to: url, options: .atomic)
            }
        } catch {
            // 不递归调 noteFailure；用系统日志留痕即可。
            log.error("activity: 失败日志写入失败: \(error)")
        }
        log.error("activity: \(message)")
    }

    /// 段 id = 文件名去扩展名（服务端用于日志与 segment 水位回显）。
    nonisolated static func segmentID(_ url: URL) -> String {
        url.deletingPathExtension().lastPathComponent
    }

    /// 末字节是否换行（读后句柄位置回到 end，调用方可直接续写）。
    private static func endsWithNewline(_ handle: FileHandle, end: UInt64) throws -> Bool {
        try handle.seek(toOffset: end - 1)
        let last = try handle.read(upToCount: 1)
        return last?.first == 0x0A
    }

    // MARK: - 编解码

    nonisolated static func encodeLine(_ e: ActivityEvent) throws -> String {
        String(decoding: try JSONEncoder().encode(e), as: UTF8.self)
    }

    nonisolated static func decodeEvent(_ data: Data) throws -> ActivityEvent {
        try JSONDecoder().decode(ActivityEvent.self, from: data)
    }

    private static func millis() -> Int { Int(Date().timeIntervalSince1970 * 1000) }
}
