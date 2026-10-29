#if os(macOS)
import Foundation
import EventKit

/// 提醒 → 本机日历的镜像：能写就建事件（带闹钟，走 iCloud 同步到手机），写不了就交回本地通知。
///
/// 规则见 `docs/design/calendar-native/design.md`：
/// - pending 且有能同步的日历 + 有权限 → 建事件，不再排本地通知
/// - 写不了 → 原样返回，交给 `NotificationManager.schedule` 走通知
/// - 提醒不再 pending（ack / cancel）→ 删事件
/// - 开关关掉 → **只停新建**，已建事件保留（那是用户日历里的东西），但也不再给它们排通知（否则双响）
/// - 用户手删了事件 → 尊重删除，不再重建，该提醒回落系统通知
///
/// 映射 `reminder.id → eventIdentifier` 与墓碑集合都存 UserDefaults（按运行环境分 key）。
@MainActor
final class ReminderCalendarMirror {

    static let shared = ReminderCalendarMirror()

    /// 设置页开关（`@AppStorage` 同一 key）
    static var enabledKey: String { LocalPaths.scoped("reminder_sync_to_calendar") }

    /// 镜像结果：还要排本地通知的提醒 + 已经写进日历的提醒 id（后者要把可能残留的本地通知撤掉）。
    struct Outcome {
        let toNotify: [Reminder]
        let mirroredIDs: Set<String>
    }

    private let mapKey = LocalPaths.scoped("reminder_calendar_map")
    private let droppedKey = LocalPaths.scoped("reminder_calendar_dropped")
    private var mapping: [String: String] = [:]
    /// 用户手动删掉日历事件的提醒 id：尊重删除，不重建
    private var dropped: Set<String> = []
    private var loaded = false
    /// 已打过「为什么跳过」日志的提醒（每条只记一次）
    private var loggedSkips: Set<String> = []
    /// 会话级负缓存：本机没有能同步的日历 → 本次运行不再逐条尝试（只打一条日志）
    private var permanentFailure: String?

    var isEnabled: Bool {
        (UserDefaults.standard.object(forKey: Self.enabledKey) as? Bool) ?? true
    }

    /// 已写入日历的提醒条数（设置页「清理」入口展示用）
    var mirroredCount: Int {
        loadIfNeeded()
        return mapping.count
    }

    // MARK: - 入口

    /// 把 pending 提醒同步进日历，返回镜像结果。
    func mirror(pending: [Reminder], total: Int) async -> Outcome {
        loadIfNeeded()
        let pendingIDs = Set(pending.map(\.id))
        pruneDropped(keeping: pendingIDs)

        let iso = ISO8601DateFormatter()
        let now = Date()
        var mirrorable: [(Reminder, Date)] = []
        for r in pending {
            guard let deadline = iso.date(from: r.deadline) else {
                logSkipOnce(r.id, "deadline 解析失败（\(r.deadline)），既写不了日历也不排通知")
                continue
            }
            guard deadline > now else {
                logSkipOnce(r.id, "已过点，不再新建事件")
                continue
            }
            guard !r.notificationBody.isEmpty else {
                logSkipOnce(r.id, "正文还没生成，等下一轮同步")
                continue
            }
            guard CalendarCore.isMirrorable(status: r.status, body: r.notificationBody, deadline: deadline, now: now) else { continue }
            mirrorable.append((r, deadline))
        }

        let needsAccess = isEnabled ? (!mapping.isEmpty || !mirrorable.isEmpty) : !mapping.isEmpty
        var authorized = CalendarStore.shared.isAuthorized
        // 关着开关时不为了维护映射去弹授权框（P3-8）；只有开关开着才请求
        if !authorized, needsAccess, isEnabled {
            authorized = await CalendarStore.shared.ensureAccess()
        }

        // 有权限才敢判断「事件是不是真没了」（读不到 ≠ 被删）
        var adopted: [String: String] = [:]
        var aliveMapped: Set<String> = []
        if authorized {
            adopted = adoptCandidates(for: mirrorable)
            aliveMapped = detectDropped(pendingIDs: pendingIDs, adopted: adopted)
            // 生命周期：提醒不再 pending（ack/取消）→ 删我们建的事件（截断时不做 diff 删除）
            if total <= pending.count {
                removeStaleEvents(keeping: pendingIDs)
            }
        }

        guard isEnabled else {
            // 关开关：不新建、不删已成的事件；只是不再给已镜像的排通知（否则双响）
            let covered = authorized ? aliveMapped : Set(mapping.keys).intersection(pendingIDs)
            return Outcome(toNotify: notifications(pending, covered: covered), mirroredIDs: covered)
        }

        guard authorized else {
            let assumed = Set(mapping.keys).intersection(pendingIDs)
            if !assumed.isEmpty {
                LogBuffer.shared.warning("[Calendar] 没有日历访问权限：\(assumed.count) 条已镜像提醒按「仍在日历里」处理，不排通知也不新建")
            }
            return Outcome(toNotify: notifications(pending, covered: assumed), mirroredIDs: assumed)
        }

        guard !mapping.isEmpty || !mirrorable.isEmpty else { return Outcome(toNotify: pending, mirroredIDs: []) }

        var mirrored = aliveMapped
        if permanentFailure == nil {
            for (reminder, deadline) in mirrorable {
                if mirrored.contains(reminder.id) { continue }
                if dropped.contains(reminder.id) {
                    LogBuffer.shared.diag("[Calendar] 事件被用户删除，跳过不重建 reminder=\(reminder.id)")
                    continue
                }
                if let hit = adopted[reminder.id] {
                    mapping[reminder.id] = hit
                    persistMapping()
                    mirrored.insert(reminder.id)
                    continue
                }
                if createEvent(for: reminder, deadline: deadline) {
                    mirrored.insert(reminder.id)
                }
            }
        }
        return Outcome(toNotify: notifications(pending, covered: mirrored), mirroredIDs: mirrored)
    }

    /// 用户显式清理：删掉所有我们建的日历事件，并把仍待办的提醒记为墓碑（回落系统通知、不再重建）。返回删除条数。
    @discardableResult
    func purgeAll() async -> Int {
        loadIfNeeded()
        let ids = Set(mapping.keys)
        guard !ids.isEmpty else { return 0 }
        guard await CalendarStore.shared.ensureAccess() else {
            LogBuffer.shared.warning("[Calendar] 没有日历访问权限，无法清理已写入的提醒事件")
            return 0
        }
        removeStaleEvents(keeping: [])
        // 不记墓碑的话下一轮同步会立刻按 pending 全量重建（用户点了「删除」转眼又长回来）
        dropped.formUnion(ids)
        persistDropped()
        return ids.count - mapping.count
    }

    // MARK: - 归属标记

    /// 从事件备注里解析归属的提醒 id（`[xhanhelper:<id>]`）。
    static func reminderID(fromNotes notes: String?) -> String? {
        guard let notes, let head = notes.range(of: "[xhanhelper:") else { return nil }
        let rest = notes[head.upperBound...]
        guard let end = rest.firstIndex(of: "]") else { return nil }
        let id = String(rest[..<end]).trimmingCharacters(in: .whitespacesAndNewlines)
        return id.isEmpty ? nil : id
    }

    /// 明确删掉某条镜像事件时调用：清映射 + 记墓碑，之后不再重建。
    func forget(reminderID: String) {
        loadIfNeeded()
        mapping[reminderID] = nil
        dropped.insert(reminderID)
        persistMapping()
        persistDropped()
        LogBuffer.shared.info("[Calendar] 事件被显式删除，之后不再重建 reminder=\(reminderID)")
    }

    // MARK: - 写

    /// 事件备注里的归属标记：映射表丢失时靠它认领旧事件（也是将来 iOS 端去重双响的依据）。
    static func marker(reminderID: String) -> String { "[xhanhelper:\(reminderID)]" }

    /// 一次区间查询把「截止时间附近、带我们标记的事件」捞成 `reminderID → eventID`，
    /// 避免每条提醒各跑一次谓词查询（主线程上跑，代价要压住）。
    private func adoptCandidates(for items: [(Reminder, Date)]) -> [String: String] {
        guard !items.isEmpty else { return [:] }
        let starts = items.map { $0.1 }
        let from = (starts.min() ?? Date()).addingTimeInterval(-60)
        let to = (starts.max() ?? Date()).addingTimeInterval(CalendarCore.reminderEventDuration + 60)
        let calendars = CalendarStore.shared.writableSyncableCalendars
        guard !calendars.isEmpty else { return [:] }
        let predicate = CalendarStore.shared.store.predicateForEvents(withStart: from, end: to, calendars: calendars)
        let events = CalendarStore.shared.store.events(matching: predicate)
        guard !events.isEmpty else { return [:] }
        var out: [String: String] = [:]
        for (reminder, _) in items {
            let marker = Self.marker(reminderID: reminder.id)
            guard let hit = events.first(where: { ($0.notes ?? "").contains(marker) }),
                  let eventID = hit.eventIdentifier else { continue }
            out[reminder.id] = eventID
        }
        return out
    }

    private func createEvent(for reminder: Reminder, deadline: Date) -> Bool {
        let cal = CalendarStore.shared
        do {
            let event = EKEvent(eventStore: cal.store)
            event.calendar = try cal.resolveCalendar(explicit: nil)
            event.title = CalendarCore.eventTitle(reminder.content)
            event.notes = reminder.notificationBody + "\n\n" + Self.marker(reminderID: reminder.id)
            event.startDate = deadline
            event.endDate = deadline.addingTimeInterval(CalendarCore.reminderEventDuration)
            event.addAlarm(EKAlarm(relativeOffset: 0))
            try cal.store.save(event, span: .thisEvent, commit: true)
            if let eventID = event.eventIdentifier {
                mapping[reminder.id] = eventID
                persistMapping()
            }
            LogBuffer.shared.info("[Calendar] 提醒写入日历 reminder=\(reminder.id) event=\(event.eventIdentifier ?? "?")")
            return true
        } catch let f as CalendarStore.Failure {
            if f.permanent, permanentFailure == nil {
                permanentFailure = f.message
                LogBuffer.shared.warning("[Calendar] 提醒不再尝试写日历（本会话），改为系统通知：\(f.message)")
            } else {
                LogBuffer.shared.warning("[Calendar] 写日历失败，改用系统通知：\(f.message)")
            }
            return false
        } catch {
            LogBuffer.shared.error("[Calendar] 写日历失败，改用系统通知：\(error)")
            return false
        }
    }

    /// 映射里有、事件却已不存在 → 先按备注标记认领（`eventIdentifier` 会因同步/改单次而变化，
    /// 不能直接判成「用户删了」）；认领不到才判用户手删：清映射、记墓碑、回落系统通知。
    /// 返回「映射里仍然真实存在（或已重新认领）」的提醒 id 集合。
    @discardableResult
    private func detectDropped(pendingIDs: Set<String>, adopted: [String: String]) -> Set<String> {
        let store = CalendarStore.shared.store
        var alive: Set<String> = []
        var changed = false
        for (reminderID, eventID) in Array(mapping) where pendingIDs.contains(reminderID) {
            if store.event(withIdentifier: eventID) != nil {
                alive.insert(reminderID)
                continue
            }
            // id 变了但事件还在（同一条提醒的标记）→ 修正映射，别当删掉
            if let newID = adopted[reminderID] {
                mapping[reminderID] = newID
                alive.insert(reminderID)
                changed = true
                LogBuffer.shared.info("[Calendar] 事件 id 变化，已重新认领 reminder=\(reminderID)")
                continue
            }
            mapping[reminderID] = nil
            dropped.insert(reminderID)
            changed = true
            LogBuffer.shared.info("[Calendar] 日历事件已被手动删除，不再重建、改为系统通知 reminder=\(reminderID)")
        }
        if changed {
            persistMapping()
            persistDropped()
        }
        return alive
    }

    /// 同一条提醒只记一次「为什么没写进日历」，避免每次前台同步都刷屏。
    private func logSkipOnce(_ reminderID: String, _ reason: String) {
        guard loggedSkips.insert(reminderID).inserted else { return }
        LogBuffer.shared.diag("[Calendar] 跳过镜像 reminder=\(reminderID)：\(reason)")
    }

    /// 提醒已 ack / cancel（不再 pending）→ 删掉对应日历事件。
    private func removeStaleEvents(keeping liveIDs: Set<String>) {
        let stale = mapping.filter { !liveIDs.contains($0.key) }
        guard !stale.isEmpty else { return }
        for (reminderID, eventID) in stale {
            if let event = CalendarStore.shared.store.event(withIdentifier: eventID) {
                do {
                    try CalendarStore.shared.store.remove(event, span: .thisEvent, commit: true)
                    LogBuffer.shared.info("[Calendar] 提醒结束，已删日历事件 reminder=\(reminderID)")
                } catch {
                    LogBuffer.shared.error("[Calendar] 删事件失败 reminder=\(reminderID): \(error)")
                    continue // 删不掉就保留映射，下一轮再试
                }
            }
            mapping[reminderID] = nil
        }
        persistMapping()
    }

    /// 已镜像（covered）的提醒不排通知，其余照旧。
    private func notifications(_ pending: [Reminder], covered: Set<String>) -> [Reminder] {
        let ids = CalendarCore.needingNotification(pendingIDs: pending.map(\.id), covered: covered)
        let keep = Set(ids)
        return pending.filter { keep.contains($0.id) }
    }

    // MARK: - 映射 / 墓碑持久化

    private func loadIfNeeded() {
        guard !loaded else { return }
        loaded = true
        if let data = UserDefaults.standard.data(forKey: mapKey) {
            do {
                mapping = try JSONDecoder().decode([String: String].self, from: data)
            } catch {
                LogBuffer.shared.error("[Calendar] 提醒↔事件映射解码失败，按空表处理：\(error)")
            }
        }
        if let data = UserDefaults.standard.data(forKey: droppedKey) {
            do {
                dropped = Set(try JSONDecoder().decode([String].self, from: data))
            } catch {
                LogBuffer.shared.error("[Calendar] 墓碑集合解码失败，按空集处理：\(error)")
            }
        }
    }

    private func persistMapping() {
        do {
            UserDefaults.standard.set(try JSONEncoder().encode(mapping), forKey: mapKey)
        } catch {
            LogBuffer.shared.error("[Calendar] 提醒↔事件映射保存失败：\(error)")
        }
    }

    private func persistDropped() {
        do {
            UserDefaults.standard.set(try JSONEncoder().encode(Array(dropped)), forKey: droppedKey)
        } catch {
            LogBuffer.shared.error("[Calendar] 墓碑集合保存失败：\(error)")
        }
    }

    /// 提醒结束后把墓碑一起清掉，避免无限增长。
    private func pruneDropped(keeping pendingIDs: Set<String>) {
        let stale = dropped.subtracting(pendingIDs)
        guard !stale.isEmpty else { return }
        dropped.subtract(stale)
        persistDropped()
    }
}
#endif
