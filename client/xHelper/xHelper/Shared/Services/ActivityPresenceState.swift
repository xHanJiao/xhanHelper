import Foundation

/// 在场/电源事件的状态机（纯逻辑，不依赖 AppKit；AppKit 接线见 `ActivityPresenceMonitor`）。
///
/// 维护两个会变的 epoch：`focusEpochId`（前台激活）、`wakeEpochId`（唤醒）；`launch_id` 由
/// `ActivityRecorder` 统一盖章（进程级固定）。只产边界点事件，长区间由服务端派生层闭合
/// （设计篇 3.2 / 6.2）。事件字典见附表 A「在场与电源」。
nonisolated struct ActivityPresenceState {
    /// 整机键鼠静止超过它算「离开」。采集端先取一个粗值，上线后按分布校准（设计篇 6.2，阈值统一进 threshold_version）。
    static let idleThresholdSeconds: Double = 300

    private(set) var focusEpochId: String?
    private(set) var wakeEpochId: String?
    /// 当前 idle 期的起点（nil = 活跃）；存起点以便 ended 时算时长。
    private(set) var idleSince: Date?

    init() {
        // 进程启动时机器是醒着的 → 先开一个 wake epoch，供首个 sleep 收尾。
        self.wakeEpochId = UUID().uuidString
    }

    func launched() -> ActivityEvent { event("client.app.launched") }
    func quit() -> ActivityEvent { event("client.app.quit") }

    /// 用于截断悬挂区间（进程启动、唤醒时写）。
    func boundary() -> ActivityEvent { event("system.boundary", attrs: ["source": .string("client")]) }

    mutating func focusGained() -> ActivityEvent {
        focusEpochId = UUID().uuidString
        return event("client.focus.gained", focusEpochId: focusEpochId)
    }

    /// 没有已知的 focus epoch（如启动后未激活就失活）则不产出——不硬造区间。
    /// 消费后置空，防同一次激活被失活两次（切窗/锁屏边界）产出两条同 epoch 的 lost。
    mutating func focusLost() -> ActivityEvent? {
        guard let epoch = focusEpochId else { return nil }
        focusEpochId = nil
        return event("client.focus.lost", focusEpochId: epoch)
    }

    /// 睡即当前 wake epoch 的终（唤醒时换新）。
    func slept() -> ActivityEvent { event("system.sleep", wakeEpochId: wakeEpochId) }

    mutating func woke() -> ActivityEvent {
        wakeEpochId = UUID().uuidString
        return event("system.wake", wakeEpochId: wakeEpochId)
    }

    func screensSlept() -> ActivityEvent { event("system.screens.sleep") }
    func screensWoke() -> ActivityEvent { event("system.screens.wake") }
    func powerOff() -> ActivityEvent { event("system.power.off") }

    /// 轮询整机空闲秒数：越过阈值发 started，回到活跃发 ended（带这一段时长）。同一段不重复发。
    mutating func idleTick(idleSeconds: Double, now: Date = Date()) -> ActivityEvent? {
        if idleSince == nil, idleSeconds >= Self.idleThresholdSeconds {
            idleSince = now.addingTimeInterval(-idleSeconds)
            // 记下当时阈值：将来校准（如 300→60）后，历史事件仍能自证按哪个阈值判的。
            return event("client.idle.started", attrs: [
                "idle_seconds": .number(idleSeconds.rounded()),
                "threshold_s": .number(Self.idleThresholdSeconds),
            ])
        }
        if let since = idleSince, idleSeconds < Self.idleThresholdSeconds {
            idleSince = nil
            let duration = max(0, now.timeIntervalSince(since))
            return event("client.idle.ended", attrs: ["idle_seconds": .number(duration.rounded())])
        }
        return nil
    }

    private func event(_ type: String,
                       focusEpochId: String? = nil,
                       wakeEpochId: String? = nil,
                       attrs: [String: JSONValue]? = nil) -> ActivityEvent {
        ActivityEvent(type: type, actor: "system", trigger: "manual",
                      focusEpochId: focusEpochId, wakeEpochId: wakeEpochId, attrs: attrs)
    }
}
