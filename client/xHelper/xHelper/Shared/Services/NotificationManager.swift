import Foundation
import UserNotifications
import Combine

@MainActor
class NotificationManager: ObservableObject {

    @Published var isAuthorized = false

    func requestAuthorization() async -> Bool {
        let center = UNUserNotificationCenter.current()
        let settings = await center.notificationSettings()
        print("[Reminder] notificationSettings: status=\(settings.authorizationStatus.rawValue)")
        guard settings.authorizationStatus == .notDetermined else {
            isAuthorized = settings.authorizationStatus == .authorized
            print("[Reminder] permission already determined: authorized=\(isAuthorized)")
            return isAuthorized
        }
        do {
            let granted = try await center.requestAuthorization(options: [.alert, .sound, .badge])
            isAuthorized = granted
            print("[Reminder] notification permission: \(granted ? "granted" : "denied")")
            return granted
        } catch {
            print("[Reminder] requestAuthorization error: \(error)")
            return false
        }
    }

    /// 全量同步：从服务端拉取 pending 提醒 → 对比本地已注册通知 → 新增/移除
    func syncWithServer(pending: [Reminder]) async {
        print("[Reminder] sync: \(pending.count) pending from server")

        if !pending.isEmpty {
            print("[Reminder] sync: has pending reminders, checking permission")
            _ = await requestAuthorization()
        }

        let center = UNUserNotificationCenter.current()
        let pendingRequests = await center.pendingNotificationRequests()
        let localIds = Set(pendingRequests.map(\.identifier))
        let serverIds = Set(pending.map(\.id))
        let now = Date()

        print("[Reminder] sync: local=\(localIds.count) server=\(serverIds.count)")

        let isoFormatter = ISO8601DateFormatter()
        var scheduledCount = 0
        var skippedCount = 0

        for reminder in pending {
            guard reminder.isPending, !reminder.isGenerating else {
                print("[Reminder] sync: skip id=\(reminder.id) status=\(reminder.status)")
                skippedCount += 1
                continue
            }
            guard !reminder.notificationBody.isEmpty else {
                print("[Reminder] sync: skip id=\(reminder.id) empty notification_body")
                skippedCount += 1
                continue
            }
            guard let deadline = isoFormatter.date(from: reminder.deadline), deadline > now else {
                print("[Reminder] sync: skip id=\(reminder.id) deadline=\(reminder.deadline) expired or unparseable")
                skippedCount += 1
                continue
            }

            if !localIds.contains(reminder.id) {
                schedule(reminder: reminder)
                scheduledCount += 1
            }
        }

        let toRemove = localIds.subtracting(serverIds)
        if !toRemove.isEmpty {
            print("[Reminder] remove: \(toRemove.count) stale local notifications")
            center.removePendingNotificationRequests(withIdentifiers: Array(toRemove))
        }

        print("[Reminder] sync: done scheduled=\(scheduledCount) skipped=\(skippedCount) removed=\(toRemove.count)")
    }

    private func schedule(reminder: Reminder) {
        let isoFormatter = ISO8601DateFormatter()
        guard let deadline = isoFormatter.date(from: reminder.deadline) else {
            print("[Reminder] schedule: bad deadline \(reminder.deadline) for \(reminder.id)")
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
                print("[Reminder] schedule failed: id=\(reminder.id) \(error)")
            } else {
                let month = components.month ?? 0
                let day = components.day ?? 0
                let hour = components.hour ?? 0
                let minute = String(format: "%02d", components.minute ?? 0)
                print("[Reminder] schedule: registered id=\(reminder.id) title=\(title) time=\(month)-\(day) \(hour):\(minute)")
            }
        }
    }
}
