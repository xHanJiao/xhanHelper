import Foundation

struct Reminder: Identifiable, Codable {
    let id: String
    let userId: String
    let content: String
    let deadline: String
    let recurrenceRule: String
    let status: String
    let notificationTitle: String
    let notificationBody: String
    let triggerMessage: String
    let triggerMessageMsgId: String
    let acknowledgedAt: String
    let mentionedAt: String
    let createdAt: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case content
        case deadline
        case recurrenceRule = "recurrence_rule"
        case status
        case notificationTitle = "notification_title"
        case notificationBody = "notification_body"
        case triggerMessage = "trigger_message"
        case triggerMessageMsgId = "trigger_message_msg_id"
        case acknowledgedAt = "acknowledged_at"
        case mentionedAt = "mentioned_at"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    var isAcknowledged: Bool {
        !acknowledgedAt.isEmpty && !acknowledgedAt.hasPrefix("0001")
    }

    var isMentioned: Bool {
        !mentionedAt.isEmpty && !mentionedAt.hasPrefix("0001")
    }

    var isPending: Bool { status == "pending" }

    var isGenerating: Bool { status == "generating" }
}

struct ReminderListResponse: Codable {
    let items: [Reminder]
    let total: Int
}

struct AckReminderResponse: Codable {
    let id: String
    let status: String
    let acknowledgedAt: String
    let triggerMessage: String
    let triggerMessageMsgId: String

    enum CodingKeys: String, CodingKey {
        case id
        case status
        case acknowledgedAt = "acknowledged_at"
        case triggerMessage = "trigger_message"
        case triggerMessageMsgId = "trigger_message_msg_id"
    }
}
