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

    func syncWithServer(pending: [Reminder]) async {
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
}
