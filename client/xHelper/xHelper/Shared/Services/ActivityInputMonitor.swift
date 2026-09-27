#if os(macOS)
import AppKit

/// 输入强度采集（macOS）：`NSEvent` 本地监听，**只覆盖本 app 窗口、不需要任何系统权限**
/// （全局键盘监听要辅助功能权限，本方案明确不采）。只计数不记内容——计数逻辑在纯逻辑的
/// `ActivityInputState` 里，这里只负责把 `keyDown` 喂进去、定时收段、发事件（附表 A）。
///
/// 采集开关由 app 层决定（本地模式才 start；与 `ActivityRecorder` 同生共死）。
@MainActor
final class ActivityInputMonitor {
    static let shared = ActivityInputMonitor()

    private var state = ActivityInputState()
    private var monitor: Any?
    private var timer: Timer?
    private var terminateToken: NSObjectProtocol?

    private static let tickSeconds: TimeInterval = 1

    private init() {}

    /// 启动采集（幂等）。
    func start() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown]) { [weak self] event in
            // 本地监听回调主线程无签名保证：主线程直接跑，其余派回主线程，别裸赌。
            if Thread.isMainThread {
                MainActor.assumeIsolated { self?.handle(event) }
            } else {
                Task { @MainActor in self?.handle(event) }
            }
            return event
        }
        timer = Timer.scheduledTimer(withTimeInterval: Self.tickSeconds, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
        // 退出时收掉还开着的段——duration_ms/chars_bucket/had_delete 是客户端独有、派生层推不出来，
        // 不补发就整段丢。尽力而为：进程可能在异步落盘前退出（照 P10 quit 的口径）。
        terminateToken = NotificationCenter.default.addObserver(
            forName: NSApplication.willTerminateNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.flushSegment() }
        }
    }

    /// 停止采集并摘掉监听；未收的段补发 `client.input.ended`。
    func stop() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        timer?.invalidate()
        timer = nil
        if let terminateToken { NotificationCenter.default.removeObserver(terminateToken) }
        terminateToken = nil
        flushSegment()
    }

    private func flushSegment() {
        if let ended = state.end() { ActivityClient.emit(ended) }
    }

    private func handle(_ event: NSEvent) {
        guard event.type == .keyDown, !event.isARepeat else { return }
        // 删除键：delete（51）/ 前向删除（117）。只判类型，不看键值内容。
        let isDelete = event.keyCode == 51 || event.keyCode == 117
        // 字符量取 characters 的长度。方向键/F 键也带私用区字符，会被略微计大——
        // 分档只到 50/200 两档，这点噪声无所谓（要精确得排除非文本键码，不值当）。
        let count = event.characters?.count ?? 0
        if let started = state.keystroke(charCount: count, isDelete: isDelete) {
            ActivityClient.emit(started)
        }
    }

    private func tick() {
        if let ended = state.tick() { ActivityClient.emit(ended) }
    }
}
#endif
