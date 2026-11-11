import Foundation

/// 一条待恢复的未发消息（文本 + 已上传的图片 id）。
struct UnsentDraftItem: Codable, Equatable {
    var text: String
    var imageIds: [String]
}

/// 会话失效（401）时暂存的未发内容；重新登录后由 ContentView 消费。
struct UnsentDraft: Codable, Equatable {
    var conversationId: String?
    var title: String?
    var items: [UnsentDraftItem]
}

enum UnsentDraftStore {
    private static let key = "xhan_unsent_draft"

    static var hasDraft: Bool {
        UserDefaults.standard.data(forKey: key) != nil
    }

    static func save(_ draft: UnsentDraft) {
        guard !draft.items.isEmpty else { return }
        do {
            UserDefaults.standard.set(try JSONEncoder().encode(draft), forKey: key)
        } catch {
            LogBuffer.shared.error("UnsentDraftStore.save failed: \(error)")
        }
    }

    /// 读取并清空：只在登录后恢复一次。
    static func take() -> UnsentDraft? {
        guard let data = UserDefaults.standard.data(forKey: key) else { return nil }
        UserDefaults.standard.removeObject(forKey: key)
        do {
            return try JSONDecoder().decode(UnsentDraft.self, from: data)
        } catch {
            LogBuffer.shared.error("UnsentDraftStore.take decode failed: \(error)")
            return nil
        }
    }
}
