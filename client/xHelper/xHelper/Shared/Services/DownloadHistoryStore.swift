import Foundation
import Combine
import WebKit
import UserNotifications

/// 浏览器下载历史（持久化）：统一记录「用户点击下载（WKDownload）」与「agent 下载（URLSession task）」
/// 两条路径，供下载记录页展示进度/状态。仅存元数据（文件名/URL/路径/大小/状态），永不落文件内容。
///
/// 状态守卫：`updateProgress / finish / cancel` 只对 `.downloading` 生效 —— 取消/失败后不会被
/// 后续 WebKit/URLSession 回调覆盖（双路径安全收尾的关键）。
@MainActor
final class DownloadHistoryStore: ObservableObject {
    static let shared = DownloadHistoryStore()

    enum State: String, Codable {
        case downloading
        case done
        case failed
        case cancelled
    }

    struct DownloadRecord: Identifiable, Codable {
        var id: UUID
        var filename: String
        var url: String
        var destinationPath: String
        var sizeBytes: Int
        var state: State
        var startedAt: Date
        var finishedAt: Date?
        var progress: Double
        /// agent 下载强制魔数媒体校验；用户点击下载不强制（贴近 Chrome）
        var enforceMedia: Bool

        /// 实时进度信息（供速度/剩余时间展示）
        var completedBytes: Int = 0
        var totalBytes: Int? = nil
        var speedBytesPerSec: Double = 0
        /// 滚动速度采样（运行时状态，不落盘）
        var lastSampleBytes: Int = 0
        var lastSampleAt: Date? = nil

        /// 运行时取消句柄（不落盘）
        var download: WKDownload?
        var task: URLSessionDownloadTask?

        enum CodingKeys: String, CodingKey {
            case id, filename, url, destinationPath, sizeBytes, state, startedAt, finishedAt, progress, enforceMedia,
                 completedBytes, totalBytes, speedBytesPerSec
        }
    }

    @Published private(set) var records: [DownloadRecord] = []

    private let storeURL: URL
    private var saveTask: Task<Void, Never>?
    private let maxRecords = 100

    private init() {
        let dir = LocalPaths.dataRoot
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        storeURL = dir.appendingPathComponent("downloads.json")
        load()
    }

    private func load() {
        guard let data = try? Data(contentsOf: storeURL),
              let decoded = try? JSONDecoder().decode([DownloadRecord].self, from: data) else { return }
        records = decoded
    }

    private func scheduleSave() {
        saveTask?.cancel()
        saveTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .milliseconds(500))
            self?.save()
        }
    }

    private func save() {
        guard let data = try? JSONEncoder().encode(records) else { return }
        try? data.write(to: storeURL, options: .atomic)
    }

    /// 注册新下载（开始），返回 record id
    @discardableResult
    func startDownload(filename: String, url: String, destination: URL,
                       download: WKDownload? = nil, task: URLSessionDownloadTask? = nil,
                       enforceMedia: Bool = false) -> UUID {
        let id = UUID()
        var record = DownloadRecord(
            id: id,
            filename: filename,
            url: url,
            destinationPath: destination.path,
            sizeBytes: 0,
            state: .downloading,
            startedAt: Date(),
            finishedAt: nil,
            progress: 0,
            enforceMedia: enforceMedia,
            download: download,
            task: task
        )
        record.download = download
        record.task = task
        records.insert(record, at: 0)
        trim()
        scheduleSave()
        return id
    }

    /// 绑定取消句柄（创建 task 后调用）
    func attachTask(id: UUID, task: URLSessionDownloadTask) {
        guard let i = records.firstIndex(where: { $0.id == id }) else { return }
        records[i].task = task
    }

    /// 下载完成时若采用服务器建议名，同步记录的文件名/路径（仍为 .downloading 时生效）
    func rename(id: UUID, filename: String, destination: URL) {
        guard let i = records.firstIndex(where: { $0.id == id }),
              records[i].state == .downloading else { return }
        records[i].filename = filename
        records[i].destinationPath = destination.path
    }

    /// 是否有进行中的下载（浏览器保活 / 下载 tab 关闭守卫用）
    var hasActiveDownloads: Bool {
        records.contains { $0.state == .downloading }
    }

    /// 字节级进度更新：算滚动瞬时速度（≥0.5s 采样防抖动）。
    func updateProgress(id: UUID, fraction: Double, completed: Int64, total: Int64) {
        guard let i = records.firstIndex(where: { $0.id == id }),
              records[i].state == .downloading else { return }
        let now = Date()
        var r = records[i]
        r.completedBytes = Int(completed)
        if total > 0 {
            r.totalBytes = Int(total)
            r.progress = min(max(Double(completed) / Double(total), 0), 1)
        } else {
            r.progress = min(max(fraction, 0), 1)
        }
        // 滚动速度采样
        if let lastAt = r.lastSampleAt {
            let dt = now.timeIntervalSince(lastAt)
            if dt >= 0.5 {
                let delta = Int64(completed) - Int64(r.lastSampleBytes)
                r.speedBytesPerSec = dt > 0 ? Double(max(delta, 0)) / dt : 0
                r.lastSampleBytes = Int(completed)
                r.lastSampleAt = now
            }
        } else {
            r.lastSampleBytes = Int(completed)
            r.lastSampleAt = now
        }
        records[i] = r
        // 进度频繁，不每次落盘；由 finish/cancel 统一保存
    }

    func finish(id: UUID, ok: Bool, sizeBytes: Int? = nil) {
        guard let i = records.firstIndex(where: { $0.id == id }),
              records[i].state == .downloading else { return }
        records[i].state = ok ? .done : .failed
        records[i].finishedAt = Date()
        records[i].progress = ok ? 1 : records[i].progress
        if let size = sizeBytes { records[i].sizeBytes = size }
        records[i].download = nil
        records[i].task = nil
        records[i].lastSampleAt = nil
        records[i].speedBytesPerSec = 0
        trim()
        scheduleSave()
        if ok {
            Self.notify(title: "下载完成", body: "\(records[i].filename) 已保存到 \(records[i].destinationPath)")
        }
    }

    func cancel(id: UUID) {
        guard let i = records.firstIndex(where: { $0.id == id }),
              records[i].state == .downloading else { return }
        records[i].state = .cancelled
        records[i].finishedAt = Date()
        records[i].download?.cancel { _ in }
        records[i].task?.cancel()
        records[i].download = nil
        records[i].task = nil
        scheduleSave()
    }

    func remove(id: UUID) {
        records.removeAll { $0.id == id }
        scheduleSave()
    }

    func clearCompleted() {
        records.removeAll { $0.state != .downloading }
        scheduleSave()
    }

    /// 重试：用记录的 URL 重新下载（走 agent 下载链，含全套安全校验）。
    /// enforceMedia 按原记录保留（agent 下载保持魔数校验，用户下载不强制）。
    func retry(id: UUID) {
        guard let i = records.firstIndex(where: { $0.id == id }) else { return }
        let record = records[i]
        remove(id: id)
        guard let url = URL(string: record.url) else { return }
        Task { @MainActor in
            _ = await BrowserToolProvider.shared.retryDownload(
                url: url,
                preferredName: record.filename,
                enforceMedia: record.enforceMedia
            )
        }
    }

    private func trim() {
        guard records.count > maxRecords else { return }
        let downloading = records.filter { $0.state == .downloading }
        let finished = records.filter { $0.state != .downloading }
        let keep = max(0, maxRecords - downloading.count)
        records = downloading + (finished.count > keep ? Array(finished.prefix(keep)) : finished)
    }

    /// 系统通知（懒请求权限，失败静默）
    static func notify(title: String, body: String) {
        Task {
            let center = UNUserNotificationCenter.current()
            _ = try? await center.requestAuthorization(options: [.alert, .sound])
            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            content.sound = .default
            try? await center.add(UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil))
        }
    }
}
