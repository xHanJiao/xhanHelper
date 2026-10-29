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
            // 只在可同步日历里匹配：本地「工作」与 iCloud「工作」并存时，别被本地那本挡住
            if let idx = matchIndex(explicitName: name, in: syncable.map(\.title)) {
                return .ok(id: syncable[idx].id)
            }
            return .explicitUnavailable(available: display(syncable))
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

    /// 按名字匹配日历：精确优先、其次包含。读（任意日历）与写（只认可同步）共用同一套匹配。
    static func matchIndex(explicitName: String, in titles: [String]) -> Int? {
        let name = explicitName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return nil }
        return titles.firstIndex { $0 == name } ?? titles.firstIndex { $0.contains(name) }
    }

    /// 已镜像（covered）的提醒不再排本地通知，其余照旧。
    static func needingNotification(pendingIDs: [String], covered: Set<String>) -> [String] {
        pendingIDs.filter { !covered.contains($0) }
    }

    // MARK: - 通知排布（纯函数，便于单测）

    struct NotificationPlan: Equatable {
        /// 需要新排的本地通知（提醒 id）
        var toSchedule: [String]
        /// 需要撤掉的本地通知（提醒 id）
        var toRemove: [String]
    }

    /// 算出这一轮要「排哪些通知、撤哪些通知」。
    ///
    /// - `eligible`：确实该响的（pending、正文已生成、还没过点）
    /// - `mirrored`：已经落进日历的 → 一律不排，且把可能残留的通知撤掉（否则日历闹钟 + 通知双响）
    /// - `truncated`：服务端还有下一页时，**不许**按「不在本页」撤通知（会误撤第 51 条起的提醒）
    static func notificationPlan(localIDs: Set<String>, pendingIDs: [String], eligible: [String],
                                 mirrored: Set<String>, truncated: Bool) -> NotificationPlan {
        var remove = localIDs.intersection(mirrored)
        if !truncated {
            remove.formUnion(localIDs.subtracting(pendingIDs))
        }
        let schedule = eligible.filter { !mirrored.contains($0) && !localIDs.contains($0) }
        return NotificationPlan(toSchedule: schedule, toRemove: remove.sorted())
    }

    // MARK: - 确认条摘要

    /// L4 确认条的人话摘要：让人看清「批的是哪条日程、什么时候、写到哪本日历」。
    /// 与 shell 一样**不做 80 字截断**（日历参数短，截断只会藏掉时间/标题）。
    static func confirmationSummary(function: String, arguments: [String: Any]) -> String {
        func str(_ key: String) -> String? {
            guard let v = arguments[key] as? String else { return nil }
            let t = v.trimmingCharacters(in: .whitespacesAndNewlines)
            return t.isEmpty ? nil : t
        }
        func stamp(_ key: String) -> String? {
            guard let raw = str(key) else { return nil }
            guard let date = isoDate(raw) else { return raw }
            return displayFormatter.string(from: date)
        }
        switch function {
        case "create_event":
            var s = "创建日程：\(str("title") ?? "(无标题)")"
            if let start = stamp("start") {
                s += " ｜ \(start)"
                if let end = stamp("end") { s += " 至 " + timeOnly(end) }
            }
            s += " ｜ " + (str("calendar") ?? "默认可同步日历")
            if (arguments["all_day"] as? Bool) == true { s += " ｜ 全天" }
            if let minutes = arguments["alarm_minutes"] as? Int {
                s += minutes < 0 ? " ｜ 不提醒" : " ｜ 提前 \(minutes) 分钟提醒"
            }
            return s
        case "update_event":
            let head = str("title").map { "修改日程：\($0)" } ?? "修改日程：\(str("event_id") ?? "")"
            var parts: [String] = []
            if let v = str("title") { parts.append("标题 → \(v)") }
            if let s = stamp("start") { parts.append("开始 → \(s)") }
            if let e = stamp("end") { parts.append("结束 → \(e)") }
            if let c = str("calendar") { parts.append("日历 → \(c)") }
            if let n = str("notes") {
                parts.append("备注 → " + (n.count > 30 ? String(n.prefix(30)) + "…" : n))
            }
            if let d = arguments["all_day"] as? Bool { parts.append(d ? "改为全天" : "改为定时") }
            if let m = arguments["alarm_minutes"] as? Int { parts.append(m < 0 ? "不提醒" : "提前 \(m) 分钟提醒") }
            return head + " ｜ " + (parts.isEmpty ? "(没改任何字段)" : parts.joined(separator: " ｜ "))
        case "delete_event":
            var s = "删除日程：\(str("event_id") ?? "")"
            if let anchor = arguments["occurrence"] as? Int {
                s += "（第 \(displayFormatter.string(from: Date(timeIntervalSince1970: TimeInterval(anchor)))) 那次）"
            }
            return s
        default:
            return arguments.map { "\($0.key)=\($0.value)" }.sorted().joined(separator: ", ")
        }
    }

    private static func isoDate(_ raw: String) -> Date? {
        let iso = ISO8601DateFormatter()
        if let d = iso.date(from: raw) { return d }
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return iso.date(from: raw)
    }

    private static let displayFormatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd HH:mm"
        return f
    }()

    /// 本地时区的展示格式（测试与实现共用，避免把断言绑死在某台机器/CI 的时区上）。
    static func displayDate(_ date: Date) -> String {
        displayFormatter.string(from: date)
    }

    private static func timeOnly(_ display: String) -> String {
        String(display.suffix(5))
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

    /// 「哪一次」的锚点：起始时间转 unix 秒（重复事件的 `eventIdentifier` 每次出现都相同，
    /// 改/删必须靠 `id + 锚点` 定位到具体那一次）。
    static func anchor(_ date: Date) -> Int {
        Int(date.timeIntervalSince1970)
    }

    /// 在候选中找出「同 id 且起始时间等于锚点（±1 秒容差）」的那个下标。
    static func occurrenceIndex(identifier: String?, anchor: Int, candidates: [(identifier: String?, start: Int)]) -> Int? {
        candidates.firstIndex {
            $0.identifier == identifier && abs($0.start - anchor) <= 1
        }
    }

    /// 这条提醒要不要落进日历：pending、正文已生成、截止时间还没过。
    static func isMirrorable(status: String, body: String, deadline: Date, now: Date) -> Bool {
        status == "pending" && !body.isEmpty && deadline > now
    }
}
