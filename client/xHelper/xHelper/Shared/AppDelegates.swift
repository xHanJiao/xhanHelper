import Foundation
import UserNotifications

extension Notification.Name {
    static let reminderTapped = Notification.Name("ReminderTapped")
    static let remindersSyncTriggered = Notification.Name("RemindersSyncTriggered")
}

/// 在 App.init() 中调用 register() 注册 UNUserNotificationCenter delegate。
/// 必须在任何 view 或 scene 创建之前执行，确保冷启动点通知时 delegate 已就位。
final class NotificationDelegateRegistrar: NSObject, UNUserNotificationCenterDelegate {

    private static var instance: NotificationDelegateRegistrar?

    static func register() {
        let d = NotificationDelegateRegistrar()
        instance = d
        UNUserNotificationCenter.current().delegate = d
        print("[Reminder] UNUserNotificationCenter delegate registered")
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                 didReceive response: UNNotificationResponse,
                                 withCompletionHandler completionHandler: @escaping () -> Void) {
        let userInfo = response.notification.request.content.userInfo
        let reminderId = userInfo["reminder_id"] as? String
        let title = response.notification.request.content.title
        let body = response.notification.request.content.body
        print("[Reminder] tap: received title=\(title) body=\(body.prefix(30)) reminder_id=\(reminderId ?? "nil")")
        if reminderId == nil {
            print("[Reminder] tap: no reminder_id in userInfo, keys=\(userInfo.keys.map { "\($0)" }.joined(separator: ","))")
        }
        NotificationCenter.default.post(
            name: .reminderTapped,
            object: nil,
            userInfo: ["reminder_id": reminderId as Any]
        )
        completionHandler()
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                 willPresent notification: UNNotification,
                                 withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        let reminderId = notification.request.content.userInfo["reminder_id"] as? String
        print("[Reminder] willPresent: id=\(reminderId ?? "nil"), showing banner")
        completionHandler([.banner, .sound])
    }
}
