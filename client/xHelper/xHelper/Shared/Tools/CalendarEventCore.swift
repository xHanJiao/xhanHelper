import Foundation

/// 日历挑选 + 事件字段的纯逻辑（不 import EventKit，SwiftPM 的 FsCore 单测可覆盖）。
/// EventKit 一侧只负责把 `EKCalendar` 折算成 `Candidate` 再调用这里。
nonisolated enum CalendarCore {

    /// 一只日历的可判定快照（来自 `EKCalendar` + `EKSource`）。
    struct Candidate: Equatable {
        let id: String
        let title: String
        let accountTitle: String
        /// "local" / "exchange" / "calDAV" / "mobileMe" / "subscribed" / "birthdays"
        let sourceType: String
        let allowsModification: Bool
        let isDefault: Bool
    }

    enum Pick: Equatable {
        case ok(id: String)
        /// 指定名字没命中 / 命中了但不可同步
        case explicitUnavailable(available: [String])
        /// 一个能同步到手机的日历都没有
        case noSyncable(available: [String])
    }

    /// 可同步到其他设备：可写，且不是本地 / 订阅 / 生日日历。
    static func isSyncable(_ c: Candidate) -> Bool {
        guard c.allowsModification else { return false }
        return !["local", "subscribed", "birthdays"].contains(c.sourceType)
    }

    /// 挑选目标日历：
    /// 1. 指定名字（精确优先、其次包含）
    /// 2. 系统默认新事件日历（可同步时）
    /// 3. iCloud 账号下第一个可同步日历
    /// 4. 其余任何可同步日历（Google / Exchange）
    /// 5. 都不行 → 报错，绝不写进本地日历（那等于「只在 Mac 上」）
    static func pick(from candidates: [Candidate], explicitName: String? = nil) -> Pick {
        let syncable = candidates.filter(isSyncable)
        let name = (explicitName ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if !name.isEmpty {
            let hit = candidates.first { $0.title == name } ?? candidates.first { $0.title.contains(name) }
            guard let hit, isSyncable(hit) else {
                return .explicitUnavailable(available: display(syncable))
            }
            return .ok(id: hit.id)
        }
        if let d = syncable.first(where: { $0.isDefault }) { return .ok(id: d.id) }
        if let ic = syncable.first(where: { $0.accountTitle == "iCloud" }) { return .ok(id: ic.id) }
        if let any = syncable.first { return .ok(id: any.id) }
        return .noSyncable(available: display(candidates.filter(\.allowsModification)))
    }

    /// 「工作（iCloud）」形式，供报错文案用。
    static func display(_ cs: [Candidate]) -> [String] {
        cs.map { "\($0.title)（\($0.accountTitle)）" }
    }

    /// 提醒正文 → 日历事件标题（单行、限长，日历标题栏放不下长句）。
    static func eventTitle(_ content: String, limit: Int = 60) -> String {
        let flat = content
            .replacingOccurrences(of: "\r\n", with: " ")
            .replacingOccurrences(of: "\n", with: " ")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard flat.count > limit else { return flat }
        return String(flat.prefix(limit)) + "…"
    }

    static let reminderEventDuration: TimeInterval = 15 * 60

    /// 这条提醒要不要落进日历：pending、正文已生成、截止时间还没过。
    static func isMirrorable(status: String, body: String, deadline: Date, now: Date) -> Bool {
        status == "pending" && !body.isEmpty && deadline > now
    }
}
