#if os(macOS)
import AppKit
import Combine
import CryptoKit
import Foundation

/// 窗口底图照片池（macOS）。池目录 `LocalPaths.dataRoot/backgrounds/`（dev/prod 隔离）。
///
/// - 导入：选目录 → **只扫顶层** → 压到长边 192 → 存内部目录（内容哈希命名，去重）。导入后与源目录无关。
/// - 轮换：`启动随机 / 切屏随机 / 定时(3·5·20min)`，单选；池空时用内置默认图、不轮换。
/// - 当前图直接给 `AppBackdrop` 用；池子只有 192px 小图，解码便宜，不预缓存整池。
@MainActor
final class BackgroundPhotoStore: ObservableObject {
    static let shared = BackgroundPhotoStore()

    enum RotationMode: String, CaseIterable, Identifiable {
        case launch, screen, min3, min5, min20
        var id: String { rawValue }

        var label: String {
            switch self {
            case .launch: return "每次启动"
            case .screen: return "切走再切回"
            case .min3: return "每 3 分钟"
            case .min5: return "每 5 分钟"
            case .min20: return "每 20 分钟"
            }
        }

        var timerInterval: TimeInterval? {
            switch self {
            case .min3: return 180
            case .min5: return 300
            case .min20: return 1200
            case .launch, .screen: return nil
            }
        }
    }

    /// 单次导入上限（防选了个几千张的目录把内存/耗时拉爆）。
    nonisolated static let maxImportCount = 300
    /// 导入输出长边（只留色块，192 足够）。
    nonisolated static let storedMaxPixel = 192

    @Published private(set) var pool: [URL] = []
    @Published private(set) var currentImage: NSImage
    @Published private(set) var isImporting = false
    @Published private(set) var importSummary: String?

    @Published var mode: RotationMode {
        didSet {
            UserDefaults.standard.set(mode.rawValue, forKey: Self.modeKey)
            reconfigureTimer()
        }
    }

    private static var modeKey: String { LocalPaths.scoped("xhan_bg_rotation") }

    private var currentURL: URL?
    private var timer: Timer?
    /// 真正干活的 detached 任务（取消它才能让 `performImport` 里的 `Task.isCancelled` 生效）。
    private var importDetached: Task<ImportResult, Never>?
    /// 等待 + 回主线程收尾的外层任务。
    private var importAwait: Task<Void, Never>?

    private var poolDir: URL {
        LocalPaths.dataRoot.appendingPathComponent("backgrounds", isDirectory: true)
    }

    private init() {
        let raw = UserDefaults.standard.string(forKey: Self.modeKey) ?? RotationMode.launch.rawValue
        self.mode = RotationMode(rawValue: raw) ?? .launch
        self.currentImage = Self.defaultImage() ?? NSImage()
        refreshPool()
        pickDifferent()
        reconfigureTimer()
    }

    // MARK: - 轮换

    /// 切屏随机（由 App 在 `applicationDidBecomeActive` 调用）。
    func rotateOnActivate() {
        guard mode == .screen else { return }
        pickDifferent()
    }

    /// 手动换一张。
    func rotateNow() { pickDifferent() }

    private func pickDifferent() {
        guard !pool.isEmpty else {
            currentURL = nil
            currentImage = Self.defaultImage() ?? NSImage()
            return
        }
        let candidates = pool.filter { $0 != currentURL }
        guard let pick = (candidates.isEmpty ? pool : candidates).randomElement() else { return }
        currentURL = pick
        if let img = NSImage(contentsOf: pick) {
            currentImage = img
        } else {
            currentImage = Self.defaultImage() ?? NSImage()
        }
    }

    private func reconfigureTimer() {
        timer?.invalidate()
        timer = nil
        guard let interval = mode.timerInterval, !pool.isEmpty else { return }
        timer = Timer.scheduledTimer(withTimeInterval: interval, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.pickDifferent() }
        }
    }

    // MARK: - 池管理

    func refreshPool() {
        let fm = FileManager.default
        // 目录不存在 = 从没导入过，属正常态（空池）；与"读失败"必须区分开。
        guard fm.fileExists(atPath: poolDir.path) else {
            pool = []
            return
        }
        do {
            pool = try Self.imageFiles(in: poolDir)
        } catch {
            // 瞬时 FS 错误：保持原池子，别让 UI 显示成"照片全没了"。
            LogBuffer.shared.error("[bg] 读取底图池失败 \(poolDir.path)：\(error.localizedDescription)")
        }
    }

    func remove(_ url: URL) {
        do {
            try FileManager.default.removeItem(at: url)
        } catch {
            LogBuffer.shared.error("[bg] 删除底图失败 \(url.lastPathComponent)：\(error.localizedDescription)")
        }
        if currentURL == url { currentURL = nil }
        refreshPool()
        // 池变空或当前被删 → 重选一张（空则回默认图）
        pickDifferent()
        reconfigureTimer()
    }

    func clear() {
        let fm = FileManager.default
        if let names = try? fm.contentsOfDirectory(atPath: poolDir.path) {
            for name in names where Self.isImageFile(name) {
                do {
                    try fm.removeItem(at: poolDir.appendingPathComponent(name))
                } catch {
                    LogBuffer.shared.error("[bg] 清空底图失败 \(name)：\(error.localizedDescription)")
                }
            }
        }
        currentURL = nil
        refreshPool()
        pickDifferent()
        reconfigureTimer()
    }

    // MARK: - 导入

    /// 从目录导入（后台执行、可取消）。`onDone` 在完成（含被取消）后回调。
    func importDirectory(_ dir: URL) {
        guard !isImporting else { return }

        // 护栏：源目录不得落在任一环境数据根内（含软链解开后再判）。
        let resolved = dir.resolvingSymlinksInPath()
        for root in LocalPaths.allDataRoots {
            let r = root.resolvingSymlinksInPath()
            if resolved.path == r.path || resolved.path.hasPrefix(r.path + "/") {
                importSummary = "不能选数据根目录或其子目录"
                return
            }
        }

        isImporting = true
        importSummary = nil
        let poolDir = self.poolDir
        // 存下 detached 句柄：否则取消只取消外层等待任务，`performImport` 里的
        // `Task.isCancelled` 永远读不到取消（点「取消」变成纯装饰）。
        let work = Task.detached(priority: .userInitiated) {
            Self.performImport(sourceDir: dir, poolDir: poolDir)
        }
        importDetached = work
        importAwait = Task { [weak self] in
            let result = await work.value
            guard let self else { return }
            self.isImporting = false
            self.importSummary = result.summary
            self.refreshPool()
            self.pickDifferent()
            self.reconfigureTimer()
            self.importDetached = nil
            self.importAwait = nil
        }
    }

    func cancelImport() {
        importDetached?.cancel()
        importAwait?.cancel()
    }

    /// 重新打开设置子页时清掉上次的导入结果。
    func clearImportSummary() { importSummary = nil }

    private struct ImportResult: Sendable {
        let imported: Int
        let skipped: Int
        let failed: Int
        let summary: String
    }

    nonisolated private static func performImport(sourceDir: URL, poolDir: URL) -> ImportResult {
        let fm = FileManager.default
        try? fm.createDirectory(at: poolDir, withIntermediateDirectories: true)
        let files = (try? imageFiles(in: sourceDir)) ?? []
        var imported = 0, skipped = 0, failed = 0
        for (i, file) in files.enumerated() {
            if Task.isCancelled { break }
            if i >= maxImportCount { break }
            guard let data = try? Data(contentsOf: file),
                  let jpeg = ImageCompressor.downscaledJPEG(data, maxPixel: storedMaxPixel) else {
                failed += 1
                continue
            }
            let hex = SHA256.hash(data: jpeg).prefix(8).map { String(format: "%02x", $0) }.joined()
            let dest = poolDir.appendingPathComponent("bg-\(hex).jpg")
            if fm.fileExists(atPath: dest.path) {
                skipped += 1
                continue
            }
            do {
                try jpeg.write(to: dest, options: .atomic)
                imported += 1
            } catch {
                failed += 1
            }
        }
        let capped = files.count > maxImportCount ? "（超过上限 \(maxImportCount)，只取前 \(maxImportCount) 张）" : ""
        let summary = "导入 \(imported) 张，跳过 \(skipped) 张（已存在），失败 \(failed) 张\(capped)"
        return ImportResult(imported: imported, skipped: skipped, failed: failed, summary: summary)
    }

    nonisolated private static func imageFiles(in dir: URL) throws -> [URL] {
        let names = try FileManager.default.contentsOfDirectory(atPath: dir.path)
        return names
            .filter { name in
                guard isImageFile(name) else { return false }
                // 排除同名目录（`foo.jpg` 文件夹不该当图片读，否则计入 failed 让人困惑）。
                var isDir: ObjCBool = false
                let path = dir.appendingPathComponent(name).path
                let exists = FileManager.default.fileExists(atPath: path, isDirectory: &isDir)
                return exists && !isDir.boolValue
            }
            .sorted()
            .map { dir.appendingPathComponent($0) }
    }

    nonisolated private static func isImageFile(_ name: String) -> Bool {
        let ext = (name as NSString).pathExtension.lowercased()
        return ["jpg", "jpeg", "png", "heic", "heif", "webp", "gif"].contains(ext)
    }

    private static func defaultImage() -> NSImage? {
        guard let image = NSImage(named: "bgDefault") else {
            LogBuffer.shared.error("[bg] 找不到默认底图 bgDefault，已回落到纯色底")
            return nil
        }
        return image
    }
}
#endif
