import Foundation

/// 客户端活动事件（上传 DTO）。字段与服务端 `activity.BatchEvent` 对齐（见附表 A 公共字段）。
///
/// 编码时 nil 字段自动省略（服务端对缺省值有兜底）；`schema_version` 由服务端补，客户端不发。
/// 生产者只填语义字段，`event_id`/`occurred_at`/客户端身份由 `ActivityRecorder` 统一补。
nonisolated struct ActivityEvent: Codable, Equatable, Sendable {
    var eventId: String
    var type: String
    var occurredAt: String
    var actor: String
    var trigger: String
    var outcome: String?
    var errorType: String?
    var errorMsg: String?
    var parentEventId: String?
    var pairId: String?
    var launchId: String?
    var focusEpochId: String?
    var componentInstanceId: String?
    var wakeEpochId: String?
    var conversationId: String?
    var turnId: String?
    var callId: String?
    var clientInstanceId: String?
    var clientBuild: String?
    var osVersion: String?
    var bizKey: String?
    var attrs: [String: JSONValue]?

    enum CodingKeys: String, CodingKey {
        case eventId = "event_id"
        case type
        case occurredAt = "occurred_at"
        case actor, trigger, outcome
        case errorType = "error_type"
        case errorMsg = "error_msg"
        case parentEventId = "parent_event_id"
        case pairId = "pair_id"
        case launchId = "launch_id"
        case focusEpochId = "focus_epoch_id"
        case componentInstanceId = "component_instance_id"
        case wakeEpochId = "wake_epoch_id"
        case conversationId = "conversation_id"
        case turnId = "turn_id"
        case callId = "call_id"
        case clientInstanceId = "client_instance_id"
        case clientBuild = "client_build"
        case osVersion = "os_version"
        case bizKey = "biz_key"
        case attrs
    }

    /// 生产者用的便捷构造：只填要填的；`eventId`/`occurredAt` 留空由 recorder 补。
    /// 其余可选字段（客户端身份等）为 nil，同样由 recorder 补。
    ///
    /// `actor`/`trigger` **无默认值**——附表 A 要求按事件区分（`system.*`/`component.*` 不是 user/manual），
    /// 给默认值会让生产者忘传时静默写错归属。
    init(type: String,
         actor: String,
         trigger: String,
         occurredAt: String = "",
         eventId: String = "",
         bizKey: String? = nil,
         outcome: String? = nil,
         errorType: String? = nil,
         errorMsg: String? = nil,
         parentEventId: String? = nil,
         pairId: String? = nil,
         launchId: String? = nil,
         focusEpochId: String? = nil,
         componentInstanceId: String? = nil,
         wakeEpochId: String? = nil,
         conversationId: String? = nil,
         turnId: String? = nil,
         callId: String? = nil,
         attrs: [String: JSONValue]? = nil) {
        self.eventId = eventId
        self.type = type
        self.occurredAt = occurredAt
        self.actor = actor
        self.trigger = trigger
        self.outcome = outcome
        self.errorType = errorType
        self.errorMsg = errorMsg
        self.parentEventId = parentEventId
        self.pairId = pairId
        self.launchId = launchId
        self.focusEpochId = focusEpochId
        self.componentInstanceId = componentInstanceId
        self.wakeEpochId = wakeEpochId
        self.conversationId = conversationId
        self.turnId = turnId
        self.callId = callId
        self.bizKey = bizKey
        self.attrs = attrs
    }
}

/// 任意 JSON 值（用于 `attrs`：数字/布尔/字符串/数组/对象）。JSONL 往返后仍等值。
nonisolated enum JSONValue: Codable, Equatable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case null
    case array([JSONValue])
    case object([String: JSONValue])

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() {
            self = .null
        } else if let b = try? c.decode(Bool.self) {
            self = .bool(b)
        } else if let n = try? c.decode(Double.self) {
            self = .number(n)
        } else if let s = try? c.decode(String.self) {
            self = .string(s)
        } else if let a = try? c.decode([JSONValue].self) {
            self = .array(a)
        } else if let o = try? c.decode([String: JSONValue].self) {
            self = .object(o)
        } else {
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "unsupported JSON value")
        }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let s): try c.encode(s)
        case .number(let n): try c.encode(n)
        case .bool(let b): try c.encode(b)
        case .null: try c.encodeNil()
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }
}

/// 上传应答（服务端 `view.JSONOK(data: IngestResult)` 的 data）。
nonisolated struct ActivityUploadAck: Decodable, Equatable, Sendable {
    let accepted: Int
    let duplicate: Int
    let watermark: String
    let watermarkKind: String

    enum CodingKeys: String, CodingKey {
        case accepted, duplicate, watermark
        case watermarkKind = "watermark_kind"
    }
}

/// 客户端身份与时间工具：`client_instance_id` 进程启动生成，构建号/系统版本随事件带上（附表 A 公共字段）。
nonisolated enum ActivityIdentity {
    /// 同机多实例（dev 与 prod 并行）靠它区分，故进程启动生成一次。
    static let clientInstanceID = UUID().uuidString

    /// L1 归属（进程存活区间）的原料，进程启动生成一次，由 recorder 盖到每条客户端事件上。
    static let launchID = UUID().uuidString

    static var clientBuild: String {
        (Bundle.main.infoDictionary?["CFBundleVersion"] as? String) ?? ""
    }

    static var osVersion: String { ProcessInfo.processInfo.operatingSystemVersionString }

    /// ISO 8601 毫秒（UTC）。与服务端 `isoMillis`（`2006-01-02T15:04:05.000Z07:00`）可互相解析。
    static func isoMillis(_ date: Date = Date()) -> String {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        f.timeZone = TimeZone(identifier: "UTC")
        return f.string(from: date)
    }
}
