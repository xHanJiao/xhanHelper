#if os(macOS)
import Foundation

/// 客户端事件的统一出口：先盖当前 `focus_epoch_id` / `wake_epoch_id`（与在场事件同一坐标），
/// 再交给 `ActivityRecorder` 落盘。浏览 / 输入 / 未来 `component.*` 等事件都走这里，
/// 免得每个生产者各自去 `ActivityPresenceMonitor` 取 epoch（附表 A「epoch 标记」）。
@MainActor
enum ActivityClient {
    static func emit(_ events: [ActivityEvent]) {
        guard !events.isEmpty else { return }
        let epochs = ActivityPresenceMonitor.shared.currentEpochs()
        let stamped = events.map { e -> ActivityEvent in
            var e = e
            if e.focusEpochId == nil { e.focusEpochId = epochs.focus }
            if e.wakeEpochId == nil { e.wakeEpochId = epochs.wake }
            return e
        }
        Task { await ActivityRecorder.shared.record(stamped) }
    }

    static func emit(_ event: ActivityEvent) { emit([event]) }
}
#endif
