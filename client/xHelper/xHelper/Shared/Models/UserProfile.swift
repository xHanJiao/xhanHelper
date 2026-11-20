import Foundation

// MARK: - 用户画像（设置页「关于我」）

/// 一条画像事实。`source`：inferred / user_stated / user / migrated。
struct ProfileFactItem: Identifiable, Decodable, Equatable {
    let id: String
    let value: String
    let source: String
    let evidenceCount: Int
    let observedAt: String?
    let lastSeenAt: String?

    enum CodingKeys: String, CodingKey {
        case id, value, source
        case evidenceCount = "evidence_count"
        case observedAt = "observed_at"
        case lastSeenAt = "last_seen_at"
    }

    /// 是不是用户自己写/改的（模型不可覆盖）。
    var isUserOwned: Bool { source == "user" }

    /// 最近证实的相对时间（如 "11-15"）。服务端下发 RFC3339（可能带纳秒），解析失败就不显示。
    var lastSeenLabel: String? {
        guard let raw = lastSeenAt ?? observedAt else { return nil }
        let frac = ISO8601DateFormatter()
        frac.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        guard let date = frac.date(from: raw) ?? plain.date(from: raw) else { return nil }
        let f = DateFormatter()
        f.dateFormat = "MM-dd"
        return f.string(from: date)
    }

    var sourceLabel: String {
        switch source {
        case "user": return "你写的"
        case "user_stated": return "对话自述"
        case "migrated": return "老数据迁移"
        case "rewrite": return "每周整合"
        default: return "自动提取"
        }
    }
}

struct ProfileFieldGroup: Identifiable, Decodable, Equatable {
    let field: String
    let kind: String
    let injectDesc: String
    let maxLen: Int
    let maxFacts: Int
    let items: [ProfileFactItem]

    var id: String { field }

    enum CodingKeys: String, CodingKey {
        case field, kind
        case injectDesc = "inject_desc"
        case maxLen = "max_len"
        case maxFacts = "max_facts"
        case items
    }
}

struct ProfileLayerGroup: Identifiable, Decodable, Equatable {
    let layer: Int
    let name: String
    let fields: [ProfileFieldGroup]

    var id: Int { layer }
}

struct ProfileResponse: Decodable {
    let groups: [ProfileLayerGroup]
}
