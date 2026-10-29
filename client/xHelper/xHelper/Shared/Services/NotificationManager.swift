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

    /// - Parameters:
    ///   - pending: 服务端返回的 pending 提醒（一次一页）
    ///   - total: 服务端总数；`total > pending.count` 说明这一页被截断了，此时**不许**按「不在本页」删东西
    func syncWithServer(pending: [Reminder], total: Int) async {
        // 先试日历：能写进日历的不再排本地通知（闹钟响在日历上，事件通过 iCloud 同步到手机）
        let truncated = total > pending.count
        var toNotify = pending
        var mirroredIDs = Set<String>()
        #if os(macOS)
        let outcome = await ReminderCalendarMirror.shared.mirror(pending: pending, total: total)
        toNotify = outcome.toNotify
        mirroredIDs = outcome.mirroredIDs
        #endif

        if !toNotify.isEmpty {
            _ = await requestAuthorization()
        }

        let center = UNUserNotificationCenter.current()
        let pendingRequests = await center.pendingNotificationRequests()
        let localIds = Set(pendingRequests.map(\.identifier))
        let serverIds = Set(toNotify.map(\.id))
        let now = Date()
        let isoFormatter = ISO8601DateFormatter()

        // 已经落进日历的提醒，之前可能排过本地通知（例如开关是刚打开的）→ 撤掉，避免同一分钟响两次
        if !mirroredIDs.isEmpty {
            let stale = localIds.intersection(mirroredIDs)
            if !stale.isEmpty {
                center.removePendingNotificationRequests(withIdentifiers: Array(stale))
            }
        }

        for reminder in toNotify {
            guard reminder.isPending, !reminder.isGenerating else { continue }
            guard !reminder.notificationBody.isEmpty else { continue }
            guard let deadline = isoFormatter.date(from: reminder.deadline), deadline > now else { continue }

            if !localIds.contains(reminder.id) {
                schedule(reminder: reminder)
            }
        }

        guard !truncated else { return }
        let toRemove = localIds.subtracting(serverIds)
        if !toRemove.isEmpty {
            center.removePendingNotificationRequests(withIdentifiers: Array(toRemove))
        }
    }

    private func schedule(reminder: Reminder) {
        let isoFormatter = ISO8601DateFormatter()
        guard let deadline = isoFormatter.date(from: reminder.deadline) else {
            LogBuffer.shared.error("[Reminder] schedule: bad deadline for \(reminder.id)")
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
            if let error = error {
                DispatchQueue.main.async {
                    LogBuffer.shared.error("[Reminder] schedule failed: id=\(reminder.id) \(error)")
                }
            }
        }
    }

    /// 立即发一条系统通知（子任务需要用户介入：ask_user / 浏览器手递手）。
    /// 与定时提醒无关，故用静态方法、不依赖实例；identifier 随机避免互相覆盖。
    static func notifyUserIntervention(message: String) {
        #if os(macOS)
        let content = UNMutableNotificationContent()
        content.title = "子任务需要你"
        content.body = message
        content.sound = .default
        let request = UNNotificationRequest(identifier: "ask_user_\(UUID().uuidString)", content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request) { _ in }
        #endif
    }
}
