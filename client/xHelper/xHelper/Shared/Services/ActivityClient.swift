#if os(macOS)
import Foundation

/// 云端活动埋点能力探测结果：由 `DeviceManager` 登录 / 校验令牌后经 `/api/health` 拉取。
/// 本地模式不依赖它（`LocalBackendManager.isActive` 直接置真）。
@MainActor
enum ActivityCapability {
    /// 服务端是否开启活动埋点（`/api/health.activity_enabled`）。默认 false：探测前/失败都不采。
    static var remoteEnabled = false

    /// GET `/api/health` 读 `activity_enabled`；网络失败 / 字段缺失 / 旧服务端（404）一律当 false
    /// （宁可少采，不可向未启用的服务端灌数据）。不静默：失败记日志。
    static func probe(baseURL: String) async -> Bool {
        guard let url = URL(string: "\(baseURL)/api/health") else { return false }
        var req = URLRequest(url: url)
        req.timeoutInterval = 5
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard (resp as? HTTPURLResponse)?.statusCode == 200 else { return false }
            return (try JSONDecoder().decode(Health.self, from: data)).activityEnabled ?? false
        } catch {
            LogBuffer.shared.error("activity capability probe failed: \(error)")
            return false
        }
    }

    private struct Health: Decodable {
        let activityEnabled: Bool?
        enum CodingKeys: String, CodingKey { case activityEnabled = "activity_enabled" }
    }
}

/// 客户端事件的统一出口：先盖当前 `focus_epoch_id` / `wake_epoch_id`（与在场事件同一坐标），
/// 再交给 `ActivityRecorder` 落盘。浏览 / 输入 / 未来 `component.*` 等事件都走这里，
/// 免得每个生产者各自去 `ActivityPresenceMonitor` 取 epoch（附表 A「epoch 标记」）。
@MainActor
enum ActivityClient {
    /// 采集是否启用（唯一出处）：本地后端接管寻址时必采；云端按服务端能力探测结果。
    /// `DeviceManager` 与这里共用，别各写一份。
    static var isEnabled: Bool { LocalBackendManager.isActive || ActivityCapability.remoteEnabled }

    static func emit(_ events: [ActivityEvent]) {
        guard isEnabled, !events.isEmpty else { return }
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
