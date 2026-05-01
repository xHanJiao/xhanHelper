import Foundation

struct Conversation: Identifiable, Codable {
    let id: String
    let deviceId: String
    let title: String
    let createdAt: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case deviceId = "device_id"
        case title
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}
