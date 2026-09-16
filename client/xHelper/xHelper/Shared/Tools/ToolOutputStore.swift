#if os(macOS)
import Foundation
import CryptoKit
import os

/// ToolOutputStore — 大体积本地工具结果的磁盘溢出存储。
///
/// 由客户端运行时（工具执行层）写入，**模型没有写权限**：超预算的工具结果全文落盘，
/// 只把预览 + `output_path` 返回给模型，模型用 `fs__grep` / `fs__read` 副本继续深挖，
/// 从而避免「结果被有损截断后内容永久丢失」。
///
/// 文件名 = `tool_<sha256>`（内容寻址），同内容天然去重，无需扫描比对。
/// 清理：7 天 TTL + 200MB 容量 LRU + 手动清空。
///
/// `nonisolated`：全部状态由 NSLock 保护、无 MainActor 依赖；允许从后台任务
/// （fs 溢出收尾的 Task.detached）直接读写，避免整文件哈希/写盘堵主线程。
nonisolated final class ToolOutputStore: @unchecked Sendable {
    static let shared = ToolOutputStore()

    /// 溢出文件名前缀（清理筛选用）。
    static let fileNamePrefix = "tool_"

    /// 溢出文件的落盘根目录（隐式只读授权用）。
    static var implicitReadOnlyRoots: [String] { [shared.rootPath] }

    private let baseURL: URL
    private let lock = NSLock()
    private var housekeepingStarted = false
    private let log = Logger(subsystem: "com.xhanHelper", category: "ToolOutputStore")

    /// 保留时长（对齐 opencode：7 天）。
    let retention: TimeInterval
    /// 目录总容量上限；超出按 mtime 最旧 LRU 删除。
    let maxTotalBytes: Int

    /// 默认单例：`Application Support/xhanHelper/tool_output`，7 天 / 200MB。
    convenience init() {
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        self.init(
            baseURL: appSupport.appendingPathComponent("xhanHelper/tool_output", isDirectory: true),
            retention: 7 * 24 * 3600,
            maxTotalBytes: 200 * 1024 * 1024
        )
    }

    /// 可注入初始化（测试用）。
    init(baseURL: URL, retention: TimeInterval, maxTotalBytes: Int) {
        self.baseURL = baseURL
        self.retention = retention
        self.maxTotalBytes = maxTotalBytes
    }

    var rootPath: String { baseURL.path }

    /// 落盘全文，返回文件绝对路径；同内容（sha256）复用已有文件。
    /// 失败返回 nil（调用方降级为原有损截断，不阻断工具返回）。
    func save(_ text: String) -> URL? {
        let data = Data(text.utf8)
        let url = baseURL.appendingPathComponent(Self.contentFileName(for: data))
        lock.lock()
        defer { lock.unlock() }
        do {
            try FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
            if !FileManager.default.fileExists(atPath: url.path) {
                try data.write(to: url, options: .atomic)
            } else {
                // 内容寻址去重：同内容复用已有文件。刷新 mtime，让 LRU 反映"最近又产生过"，
                // 避免刚用到的副本因旧 mtime 被提前清掉。
                try? FileManager.default.setAttributes([.modificationDate: Date()], ofItemAtPath: url.path)
            }
            return url
        } catch {
            log.error("save failed: \(error.localizedDescription, privacy: .public)")
            return nil
        }
    }

    /// 清理：删 mtime 超期的文件；仍超容量上限时按 mtime 最旧 LRU 删。
    func cleanup() {
        lock.lock()
        defer { lock.unlock() }
        let fm = FileManager.default
        guard let entries = try? fm.contentsOfDirectory(
            at: baseURL,
            includingPropertiesForKeys: [.contentModificationDateKey, .fileSizeKey]
        ) else { return }

        let now = Date()
        var kept: [(url: URL, mtime: Date, size: Int)] = []
        for url in entries where url.lastPathComponent.hasPrefix(Self.fileNamePrefix) {
            let values = try? url.resourceValues(forKeys: [.contentModificationDateKey, .fileSizeKey])
            let mtime = values?.contentModificationDate ?? .distantPast
            let size = values?.fileSize ?? 0
            if now.timeIntervalSince(mtime) > retention {
                try? fm.removeItem(at: url)
            } else {
                kept.append((url, mtime, size))
            }
        }

        var total = kept.reduce(0) { $0 + $1.size }
        if total > maxTotalBytes {
            for item in kept.sorted(by: { $0.mtime < $1.mtime }) {
                if total <= maxTotalBytes { break }
                try? fm.removeItem(at: item.url)
                total -= item.size
            }
        }
    }

    /// 清空整个目录（设置页手动清理用）。
    func clearAll() {
        lock.lock()
        defer { lock.unlock() }
        try? FileManager.default.removeItem(at: baseURL)
    }

    /// 启动 + 每小时 housekeeping（首次调用去重）。
    func startHousekeeping() {
        lock.lock()
        if housekeepingStarted {
            lock.unlock()
            return
        }
        housekeepingStarted = true
        lock.unlock()

        Task.detached(priority: .utility) { [weak self] in
            guard let self else { return }
            self.cleanup()
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 3600 * 1_000_000_000)
                self.cleanup()
            }
        }
    }

    /// 内容寻址文件名：`tool_<sha256>`（同内容复用同一文件）。
    static func contentFileName(for data: Data) -> String {
        "\(fileNamePrefix)\(sha256Hex(data))"
    }

    private static func sha256Hex(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }
}
#endif
