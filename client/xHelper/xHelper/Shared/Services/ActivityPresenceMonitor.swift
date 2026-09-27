#if os(macOS)
import AppKit

/// 在场/电源事件采集（macOS）：把系统与进程生命周期信号转成事件，交给 `ActivityRecorder` 落盘。
///
/// 只产边界点事件，长区间由服务端派生层闭合（设计篇 3.2 / 6.2）。
/// idle 用 `CGEventSourceSecondsSinceLastEventType`（整机键鼠级，免辅助功能权限），轮询判定。
/// 采集开关由 app 层决定（本地模式才 start；与 `ActivityRecorder` 同生共死）。
@MainActor
final class ActivityPresenceMonitor {
    static let shared = ActivityPresenceMonitor()

    private var state = ActivityPresenceState()
    private var tokens: [(NotificationCenter, NSObjectProtocol)] = []
    private var idleTask: Task<Void, Never>?
    private var started = false
    private var announcedLaunch = false

    private static let idlePollSeconds = 10

    // kCGAnyInputEventType = (CGEventType)~0，Swift 未导入为枚举成员，故手构。
    private static let anyInputEventType = CGEventType(rawValue: ~0)!

    private init() {}

    /// 启动采集（幂等）：进程启动事件 + boundary，注册各通知与 idle 轮询。
    func start() {
        guard !started else { return }
        started = true
        subscribe()
        if !announcedLaunch {
            // launched 是「进程启动」的事实，重启采集不再重发；boundary 每次启动都写（截断悬挂区间）。
            announcedLaunch = true
            emit([state.launched(), state.boundary()])
        } else {
            emit(state.boundary())
        }
        startIdlePolling()
    }

    /// 停止采集并摘掉所有监听（登出/关闭本地采集）。
    func stop() {
        guard started else { return }
        started = false
        for (center, token) in tokens { center.removeObserver(token) }
        tokens.removeAll()
        idleTask?.cancel()
        idleTask = nil
    }

    /// 当前 epoch 快照：供其它客户端事件（浏览/输入）盖上同一坐标（附表 A「epoch 标记」）。
    func currentEpochs() -> (focus: String?, wake: String?) {
        (state.focusEpochId, state.wakeEpochId)
    }

    // MARK: 通知接线

    private func subscribe() {
        let nc = NotificationCenter.default
        let ws = NSWorkspace.shared.notificationCenter
        observe(nc, NSApplication.didBecomeActiveNotification) { self.emit(self.state.focusGained()) }
        observe(nc, NSApplication.didResignActiveNotification) { self.emit(self.state.focusLost()) }
        observe(nc, NSApplication.willTerminateNotification) { self.emit(self.state.quit()) }
        observe(ws, NSWorkspace.willSleepNotification) { self.emitSleep() }
        observe(ws, NSWorkspace.didWakeNotification) { self.emitWake() }
        observe(ws, NSWorkspace.screensDidSleepNotification) { self.emit(self.state.screensSlept()) }
        observe(ws, NSWorkspace.screensDidWakeNotification) { self.emit(self.state.screensWoke()) }
        observe(ws, NSWorkspace.willPowerOffNotification) { self.emit(self.state.powerOff()) }
    }

    private func observe(_ center: NotificationCenter,
                         _ name: Notification.Name,
                         _ handler: @MainActor @Sendable @escaping () -> Void) {
        let token = center.addObserver(forName: name, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { handler() }
        }
        tokens.append((center, token))
    }

    // MARK: 产出

    private func emit(_ event: ActivityEvent?) {
        guard let event else { return }
        Task { await ActivityRecorder.shared.record(event) }
    }

    /// 成对事件走一次 `record`（保序落盘）；单独发会各起一个 Task，理论可乱序。
    private func emit(_ events: [ActivityEvent?]) {
        let batch = events.compactMap { $0 }
        guard !batch.isEmpty else { return }
        Task { await ActivityRecorder.shared.record(batch) }
    }

    private func emitSleep() {
        emit([state.slept(), state.boundary()])
    }

    private func emitWake() {
        emit([state.woke(), state.boundary()])
    }

    // MARK: idle 轮询

    private func startIdlePolling() {
        idleTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(ActivityPresenceMonitor.idlePollSeconds))
                guard let self else { return }
                self.idleTick()
            }
        }
    }

    private func idleTick() {
        let seconds = CGEventSource.secondsSinceLastEventType(.combinedSessionState, eventType: Self.anyInputEventType)
        emit(state.idleTick(idleSeconds: seconds))
    }
}
#endif
