import Foundation
import UserNotifications
import Combine

@MainActor
class NotificationManager: ObservableObject {

    @Published var isAuthorized = false

    func requestAuthorization() async -> Bool {
        let center = UNUserNotificationCenter.current()
        let settings = await center.notificationSettings()
        guard settings.authorizationStatus == .notDetermined else {
            isAuthorized = settings.authorizationStatus == .authorized
            return isAuthorized
        }
        do {
            let granted = try await center.requestAuthorization(options: [.alert, .sound, .badge])
            isAuthorized = granted
            LogBuffer.shared.info("[Reminder] notification permission: \(granted ? "granted" : "denied")")
            return granted
        } catch {
            LogBuffer.shared.error("[Reminder] requestAuthorization error: \(error)")
            return false
        }
    }

    /// 全量同步：从服务端拉取 pending 提醒 → 对比本地已注册通知 → 新增/移除
    func syncWithServer(pending: [Reminder]) async {
        LogBuffer.shared.debug("[Reminder] sync: \(pending.count) pending from server")

        if !pending.isEmpty {
            _ = await requestAuthorization()
        }

        let center = UNUserNotificationCenter.current()
        let pendingRequests = await center.pendingNotificationRequests()
        let localIds = Set(pendingRequests.map(\.identifier))
        let serverIds = Set(pending.map(\.id))
        let now = Date()

        let isoFormatter = ISO8601DateFormatter()

        for reminder in pending {
            guard reminder.isPending, !reminder.isGenerating else { continue }
            guard !reminder.notificationBody.isEmpty else { continue }
            guard let deadline = isoFormatter.date(from: reminder.deadline), deadline > now else { continue }

            if !localIds.contains(reminder.id) {
                schedule(reminder: reminder)
            }
        }

        let toRemove = localIds.subtracting(serverIds)
        for id in toRemove {
            center.removePendingNotificationRequests(withIdentifiers: [id])
            LogBuffer.shared.debug("[Reminder] remove: local notification id=\(id) (gone from server)")
        }
    }

    private func schedule(reminder: Reminder) {
        let isoFormatter = ISO8601DateFormatter()
        guard let deadline = isoFormatter.date(from: reminder.deadline) else {
            LogBuffer.shared.error("[Reminder] schedule: bad deadline \(reminder.deadline) for \(reminder.id)")
            return
        }

        let content = UNMutableNotificationContent()
        content.title = reminder.notificationTitle.isEmpty ? "辽辽提醒" : reminder.notificationTitle
        content.body = reminder.notificationBody
        content.sound = .default
        content.userInfo = ["reminder_id": reminder.id]

        let components = Calendar.current.dateComponents([.year, .month, .day, .hour, .minute], from: deadline)
        let trigger = UNCalendarNotificationTrigger(dateMatching: components, repeats: false)

        let request = UNNotificationRequest(identifier: reminder.id, content: content, trigger: trigger)
        UNUserNotificationCenter.current().add(request) { error in
            let title = reminder.notificationTitle.isEmpty ? "辽辽提醒" : reminder.notificationTitle
            if let error = error {
                DispatchQueue.main.async {
                    LogBuffer.shared.error("[Reminder] schedule failed: id=\(reminder.id) \(error)")
                }
            } else {
                let month = components.month ?? 0
                let day = components.day ?? 0
                let hour = components.hour ?? 0
                let minute = String(format: "%02d", components.minute ?? 0)
                DispatchQueue.main.async {
                    LogBuffer.shared.debug("[Reminder] schedule: registered id=\(reminder.id) title=\(title) time=\(month)-\(day) \(hour):\(minute)")
                }
            }
        }
    }
}
