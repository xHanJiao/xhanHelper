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
        //
        // shortcut: 镜像只在 macOS 做。将来真上 iOS 客户端时，iOS 会为同一条提醒再排一次本地通知，
        // 与已同步过去的日历闹钟双响；届时要靠事件备注里的 [xhanhelper:<reminder_id>] 反查去重。
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
        let localIds = Set((await center.pendingNotificationRequests()).map(\.identifier))
        let now = Date()
        let isoFormatter = ISO8601DateFormatter()

        let eligible = toNotify.filter { reminder in
            guard reminder.isPending, !reminder.isGenerating else { return false }
            guard !reminder.notificationBody.isEmpty else {
                LogBuffer.shared.diag("[Reminder] 正文还没生成，本轮不排通知 id=\(reminder.id)")
                return false
            }
            guard let deadline = isoFormatter.date(from: reminder.deadline), deadline > now else { return false }
            return true
        }
        // 排布规则（含「已镜像不排」「截断不撤」）在 `CalendarCore.notificationPlan`，有单测
        let plan = CalendarCore.notificationPlan(
            localIDs: localIds,
            pendingIDs: toNotify.map(\.id),
            eligible: eligible.map(\.id),
            mirrored: mirroredIDs,
            truncated: truncated)

        if !plan.toRemove.isEmpty {
            center.removePendingNotificationRequests(withIdentifiers: plan.toRemove)
        }
        // offset 分页 + created_at DESC 时，两页之间新建提醒会让边界项重复 → 必须去重，否则会 trap
        let byID = Dictionary(toNotify.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        for id in plan.toSchedule {
            guard let reminder = byID[id] else { continue }
            schedule(reminder: reminder)
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
