#if os(macOS)
import Foundation
import EventKit

/// 本机日历工具（namespace `calendar`）：读 / 查 / 建 / 改 / 删日历事件。
///
/// 只在 Mac 客户端执行（EventKit 拿的是这台机器的日历库，走 iCloud 同步到手机）。
/// 风险等级：读 = L2 免确认；建/改/删 = L4 红色必弹（见 `ToolRiskLevels.byTool`）。
final class CalendarToolProvider: LocalToolProvider {

    let namespace = "calendar"

    var groupDescription: String? {
        "日历：查看/搜索本机日程，创建/修改/删除事件（iCloud 账号下的日历会同步到手机）"
    }

    func toolSchemas() -> [LocalToolSchema] {
        [
            LocalToolSchema(
                name: "list_events",
                description: """
                查看某个时间窗口内的日历事件（按开始时间升序）。
                用户问「我明天有什么安排」「这周几个会」「下周三下午空不空」时用本工具。
                时间一律用带时区的 ISO8601，如 2026-10-30T00:00:00+08:00；默认返回 50 条（上限 200）。
                每行末尾的 id= 和 t=（起始 unix 秒）是改/删时的定位依据，重复事件必须两者一起传。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "start": ["type": "string", "description": "窗口开始，ISO8601（含时区）"],
                        "end": ["type": "string", "description": "窗口结束，ISO8601（含时区）"],
                        "calendar": ["type": "string", "description": "只看某本日历（按名称，可省略）"],
                        "limit": ["type": "integer", "description": "最多返回条数，默认 50"],
                    ],
                    "required": ["start", "end"],
                ],
                isReadOnly: true
            ),
            LocalToolSchema(
                name: "search_events",
                description: """
                按关键字搜索日历事件（标题/备注/地点包含即命中）。
                用户问「我什么时候跟张三开会」「上次那个体检预约是哪天」时用本工具。
                默认搜索前后 30 天，可用 days_back / days_forward 调整；默认返回 30 条（上限 200）。
                每行末尾的 id= 和 t= 是改/删时的定位依据，重复事件必须两者一起传。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "query": ["type": "string", "description": "关键字"],
                        "days_back": ["type": "integer", "description": "往前搜几天，默认 30"],
                        "days_forward": ["type": "integer", "description": "往后搜几天，默认 30"],
                        "limit": ["type": "integer", "description": "最多返回条数，默认 30"],
                    ],
                    "required": ["query"],
                ],
                isReadOnly: true
            ),
            LocalToolSchema(
                name: "create_event",
                description: """
                在日历上创建一个事件，返回 event_id（之后修改/删除要用）。
                用户说「帮我记到日历上」「下周三下午三点安排评审」时用本工具。
                参数里的时间必须是带时区的 ISO8601。默认写进能同步到手机的日历（iCloud 优先），
                可用 calendar 指定日历名。会弹确认条，用户批准后才真正写入。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "title": ["type": "string", "description": "事件标题"],
                        "start": ["type": "string", "description": "开始时间，ISO8601（含时区）"],
                        "end": ["type": "string", "description": "结束时间，ISO8601；省略则开始后 15 分钟"],
                        "calendar": ["type": "string", "description": "目标日历名，省略用默认可同步日历"],
                        "notes": ["type": "string", "description": "备注"],
                        "all_day": ["type": "boolean", "description": "是否全天事件"],
                        "alarm_minutes": ["type": "integer", "description": "提前几分钟提醒，0 = 到点提醒，默认 0，-1 = 不提醒"],
                    ],
                    "required": ["title", "start"],
                ],
                isReadOnly: false
            ),
            LocalToolSchema(
                name: "update_event",
                description: """
                修改一个已存在的日历事件（先用 list_events / search_events 拿到 id 与 t）。
                只传要改的字段。重复事件的修改只作用于该次，因此必须同时传 occurrence（= 列表里的 t）。
                不能给其他人发邀请（EventKit 不支持）。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "event_id": ["type": "string", "description": "要修改的事件 ID"],
                        "occurrence": ["type": "integer", "description": "哪一次：列表行尾的 t=（unix 秒）。重复事件必传"],
                        "title": ["type": "string", "description": "新标题"],
                        "start": ["type": "string", "description": "新开始时间，ISO8601"],
                        "end": ["type": "string", "description": "新结束时间，ISO8601"],
                        "calendar": ["type": "string", "description": "移到另一本日历（名称）"],
                        "notes": ["type": "string", "description": "新备注"],
                        "all_day": ["type": "boolean", "description": "改成全天 / 改回定时"],
                        "alarm_minutes": ["type": "integer", "description": "提前几分钟提醒：0 = 到点，-1 = 不提醒。不传则保持原闹钟"],
                    ],
                    "required": ["event_id"],
                ],
                isReadOnly: false
            ),
            LocalToolSchema(
                name: "delete_event",
                description: """
                删除一个日历事件（先用 list_events / search_events 拿到 id 与 t）。
                重复事件的删除只作用于该次，因此必须同时传 occurrence（= 列表里的 t）。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "event_id": ["type": "string", "description": "要删除的事件 ID"],
                        "occurrence": ["type": "integer", "description": "哪一次：列表行尾的 t=（unix 秒）。重复事件必传"],
                    ],
                    "required": ["event_id"],
                ],
                isReadOnly: false
            ),
        ]
    }

    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        let cal = CalendarStore.shared
        guard await cal.ensureAccess() else {
            return Self.error("没有日历访问权限。请在「系统设置 → 隐私与安全性 → 日历」里允许 xhanHelper，或到设置页点「请求日历权限」")
        }
        do {
            switch tool {
            case "list_events": return try listEvents(arguments, cal)
            case "search_events": return try searchEvents(arguments, cal)
            case "create_event": return try createEvent(arguments, cal)
            case "update_event": return try updateEvent(arguments, cal)
            case "delete_event": return try deleteEvent(arguments, cal)
            default: return Self.error("未知函数: \(tool)")
            }
        } catch let f as CalendarStore.Failure {
            return Self.error(f.message)
        } catch {
            LogBuffer.shared.error("[Calendar] \(tool) failed: \(error)")
            return Self.error("日历操作失败：\(error.localizedDescription)")
        }
    }

    // MARK: - 读

    private func listEvents(_ args: [String: Any], _ cal: CalendarStore) throws -> LocalToolResult {
        let start = try Self.parseDate(args["start"] as? String, name: "start")
        let end = try Self.parseDate(args["end"] as? String, name: "end")
        guard end > start else { throw CalendarStore.Failure(message: "end 必须晚于 start") }
        let limit = min(max((args["limit"] as? Int) ?? 50, 1), 200)
        var calendars: [EKCalendar] = []
        if let name = args["calendar"] as? String, !name.isEmpty {
            calendars = [try cal.resolveCalendarForReading(explicit: name)]
        }
        let predicate = cal.store.predicateForEvents(withStart: start, end: end, calendars: calendars.isEmpty ? nil : calendars)
        let events = cal.store.events(matching: predicate).sorted { $0.startDate < $1.startDate }
        return Self.textResult(events: Array(events.prefix(limit)), total: events.count, cal: cal)
    }

    private func searchEvents(_ args: [String: Any], _ cal: CalendarStore) throws -> LocalToolResult {
        guard let query = (args["query"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines), !query.isEmpty else {
            return Self.error("缺少 query 参数")
        }
        let now = Date()
        let from = now.addingTimeInterval(-Double((args["days_back"] as? Int) ?? 30) * 86400)
        let to = now.addingTimeInterval(Double((args["days_forward"] as? Int) ?? 30) * 86400)
        let limit = min(max((args["limit"] as? Int) ?? 30, 1), 200)
        let predicate = cal.store.predicateForEvents(withStart: from, end: to, calendars: nil)
        let hits = cal.store.events(matching: predicate).filter { e in
            let hay = [e.title ?? "", e.notes ?? "", e.location ?? ""].joined(separator: "\n")
            return hay.localizedCaseInsensitiveContains(query)
        }.sorted { $0.startDate > $1.startDate }
        return Self.textResult(events: Array(hits.prefix(limit)), total: hits.count, cal: cal)
    }

    // MARK: - 写

    private func createEvent(_ args: [String: Any], _ cal: CalendarStore) throws -> LocalToolResult {
        guard let title = (args["title"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines), !title.isEmpty else {
            return Self.error("缺少 title 参数")
        }
        let start = try Self.parseDate(args["start"] as? String, name: "start")
        let allDay = (args["all_day"] as? Bool) ?? false
        let end: Date
        if let endRaw = args["end"] as? String {
            end = try Self.parseDate(endRaw, name: "end")
        } else {
            end = start.addingTimeInterval(allDay ? 86400 : CalendarCore.reminderEventDuration)
        }
        guard end > start else { throw CalendarStore.Failure(message: "end 必须晚于 start") }

        let event = EKEvent(eventStore: cal.store)
        event.calendar = try cal.resolveCalendar(explicit: args["calendar"] as? String)
        event.title = title
        event.startDate = start
        event.endDate = end
        event.isAllDay = allDay
        if allDay { Self.normalizeAllDay(event) }
        if let notes = args["notes"] as? String, !notes.isEmpty { event.notes = notes }
        let alarmMinutes = (args["alarm_minutes"] as? Int) ?? 0
        if alarmMinutes >= 0 {
            event.addAlarm(EKAlarm(relativeOffset: -Double(alarmMinutes) * 60))
        }
        try cal.store.save(event, span: .thisEvent, commit: true)
        LogBuffer.shared.info("[Calendar] create_event: \(title) @\(event.calendar?.title ?? "")")
        return Self.json(["ok": true, "event_id": event.eventIdentifier ?? "", "summary": cal.describe(event)])
    }

    private func updateEvent(_ args: [String: Any], _ cal: CalendarStore) throws -> LocalToolResult {
        let event = try Self.lookup(args, cal)
        if let title = args["title"] as? String, !title.isEmpty { event.title = title }
        if let notes = args["notes"] as? String { event.notes = notes }
        if let raw = args["start"] as? String {
            let start = try Self.parseDate(raw, name: "start")
            let duration = max(event.endDate.timeIntervalSince(event.startDate), 60)
            event.startDate = start
            if args["end"] as? String == nil { event.endDate = start.addingTimeInterval(duration) }
        }
        if args["end"] as? String != nil {
            event.endDate = try Self.parseDate(args["end"] as? String, name: "end")
        }
        if let allDay = args["all_day"] as? Bool {
            event.isAllDay = allDay
            if allDay { Self.normalizeAllDay(event) }
        }
        if let minutes = args["alarm_minutes"] as? Int {
            for alarm in event.alarms ?? [] { event.removeAlarm(alarm) }
            if minutes >= 0 { event.addAlarm(EKAlarm(relativeOffset: -Double(minutes) * 60)) }
        }
        if let name = args["calendar"] as? String, !name.isEmpty {
            event.calendar = try cal.resolveCalendar(explicit: name)
        }
        guard event.endDate > event.startDate else { throw CalendarStore.Failure(message: "end 必须晚于 start") }
        try cal.store.save(event, span: .thisEvent, commit: true)
        LogBuffer.shared.info("[Calendar] update_event: \(event.eventIdentifier ?? "")")
        return Self.json(["ok": true, "event_id": event.eventIdentifier ?? "", "summary": cal.describe(event)])
    }

    private func deleteEvent(_ args: [String: Any], _ cal: CalendarStore) throws -> LocalToolResult {
        let event = try Self.lookup(args, cal)
        let id = event.eventIdentifier ?? ""
        let mirrorReminderID = ReminderCalendarMirror.reminderID(fromNotes: event.notes)
        try cal.store.remove(event, span: .thisEvent, commit: true)
        LogBuffer.shared.info("[Calendar] delete_event: \(id)")
        var payload: [String: Any] = ["ok": true, "event_id": id]
        if let mirrorReminderID {
            // 提醒镜像的事件：尊重删除，记墓碑后不再重建；那提醒会回落成系统通知
            ReminderCalendarMirror.shared.forget(reminderID: mirrorReminderID)
            payload["note"] = "这是提醒镜像生成的事件，已记下删除、不会再自动重建；对应提醒改为系统通知"
        }
        return Self.json(payload)
    }

    /// 按 `event_id`（+ `occurrence` = 起始 unix 秒）定位事件。
    ///
    /// 重复事件每次出现的 `eventIdentifier` 相同，`store.event(withIdentifier:)` 拿回的是系列首条，
    /// 直接 `.thisEvent` 改/删会落到错的那一次 —— 所以重复事件必须带锚点，用窗口查询挑出那一次。
    private static func lookup(_ args: [String: Any], _ cal: CalendarStore) throws -> EKEvent {
        guard let id = (args["event_id"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines), !id.isEmpty else {
            throw CalendarStore.Failure(message: "缺少 event_id 参数")
        }
        if let anchor = args["occurrence"] as? Int {
            let start = Date(timeIntervalSince1970: TimeInterval(anchor))
            let predicate = cal.store.predicateForEvents(
                withStart: start.addingTimeInterval(-1), end: start.addingTimeInterval(1), calendars: nil)
            // shortcut: 跨时区或被移动过的 occurrence 可能定位不到，届时报错让模型重新 list_events
            let candidates = cal.store.events(matching: predicate)
            let index = CalendarCore.occurrenceIndex(
                identifier: id,
                anchor: anchor,
                candidates: candidates.map { ($0.eventIdentifier, CalendarCore.anchor($0.startDate)) })
            guard let index else {
                throw CalendarStore.Failure(message: "找不到 id=\(id) 在 t=\(anchor) 那次（可能已被移动/删除），请重新 list_events 取最新 t")
            }
            return candidates[index]
        }
        guard let event = cal.store.event(withIdentifier: id) else {
            throw CalendarStore.Failure(message: "找不到事件 \(id)（可能已被删除或日历被移除）")
        }
        if event.hasRecurrenceRules {
            throw CalendarStore.Failure(message: "这是重复事件，改/删必须带 occurrence（列表行尾的 t=），否则会落到系列第一次")
        }
        return event
    }

    // MARK: - 全天事件归一

    /// 全天事件归一到「当天 00:00 → 结束日 00:00」（日历感知，夏令时切换日也是整一天）。
    /// 多日全天保留跨度：只把首尾落到日界，不压成一天。
    private static func normalizeAllDay(_ event: EKEvent) {
        let cal = Calendar.current
        let startDay = cal.startOfDay(for: event.startDate)
        let endDay = cal.startOfDay(for: event.endDate)
        event.startDate = startDay
        if endDay > startDay {
            event.endDate = endDay
        } else {
            event.endDate = cal.date(byAdding: .day, value: 1, to: startDay) ?? startDay.addingTimeInterval(86400)
        }
    }

    // MARK: - 结果封装
    private static func textResult(events: [EKEvent], total: Int, cal: CalendarStore) -> LocalToolResult {
        let lines = events.map { cal.describe($0) }
        var text = lines.joined(separator: "\n")
        if total > events.count {
            text += "\n…共 \(total) 条，只显示前 \(events.count) 条（缩小时间范围或提高 limit）"
        }
        if text.isEmpty { text = "该范围内没有事件" }
        // 与客户端工具注入预算（20KB）对齐，别把整页事件塞爆
        if text.utf8.count > 15000 {
            text = String(text.prefix(5000)) + "\n…结果过大已截断，请缩小时间范围"
        }
        return json(["count": lines.count, "total": total, "events": text])
    }

    private static func json(_ obj: [String: Any]) -> LocalToolResult {
        guard let data = try? JSONSerialization.data(withJSONObject: obj) else {
            return error("结果序列化失败")
        }
        return LocalToolResult(serverResult: data, serverError: nil, clientAction: nil)
    }

    private static func error(_ msg: String) -> LocalToolResult {
        LocalToolResult(serverResult: nil, serverError: msg, clientAction: nil)
    }

    // MARK: - 日期解析

    /// 只认带时区的 ISO8601（服务端 `NormalizeDatesStep` 会把相对时间算成绝对时间）。
    static func parseDate(_ raw: String?, name: String) throws -> Date {
        guard let raw = raw?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty else {
            throw CalendarStore.Failure(message: "缺少 \(name) 参数（ISO8601，含时区）")
        }
        let iso = ISO8601DateFormatter()
        if let d = iso.date(from: raw) { return d }
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = iso.date(from: raw) { return d }
        throw CalendarStore.Failure(message: "\(name) 不是合法的 ISO8601 时间：\(raw)（例：2026-10-30T14:00:00+08:00）")
    }
}
#endif
