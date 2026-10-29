#if os(macOS)
import Foundation
import EventKit

/// 提醒 → 本机日历的镜像：能写就建事件（带闹钟，走 iCloud 同步到手机），写不了就交回本地通知。
///
/// 规则见 `docs/design/calendar-native/design.md`：
/// - pending 且有能同步的日历 + 有权限 → 建事件，不再排本地通知
/// - 写不了 → 原样返回，交给 `NotificationManager.schedule` 走通知
/// - 提醒不再 pending（ack / cancel）→ 删事件
///
/// 映射 `reminder.id → eventIdentifier` 存 UserDefaults（按运行环境分 key）。
@MainActor
final class ReminderCalendarMirror {

    static let shared = ReminderCalendarMirror()

    /// 设置页开关（`@AppStorage` 同一 key）
    static var enabledKey: String { LocalPaths.scoped("reminder_sync_to_calendar") }

    private let mapKey = LocalPaths.scoped("reminder_calendar_map")
    private var mapping: [String: String] = [:]
    private var mappingLoaded = false
    /// 会话级负缓存：本机没有能同步的日历 → 本次运行不再逐条尝试（只打一条日志）
    private var permanentFailure: String?

    var isEnabled: Bool {
        (UserDefaults.standard.object(forKey: Self.enabledKey) as? Bool) ?? true
    }

    /// 镜像结果：还要排本地通知的提醒 + 已经写进日历的提醒 id（后者要把可能残留的本地通知撤掉）。
    struct Outcome {
        let toNotify: [Reminder]
        let mirroredIDs: Set<String>
    }

    /// 把 pending 提醒同步进日历，返回镜像结果。
    func mirror(pending: [Reminder], total: Int) async -> Outcome {
        guard isEnabled else { return Outcome(toNotify: pending, mirroredIDs: []) }
        loadMapping()

        let iso = ISO8601DateFormatter()
        let now = Date()
        var mirrorable: [(Reminder, Date)] = []
        for r in pending {
            guard let deadline = iso.date(from: r.deadline),
                  CalendarCore.isMirrorable(status: r.status, body: r.notificationBody, deadline: deadline, now: now)
            else { continue }
            mirrorable.append((r, deadline))
        }

        guard !mapping.isEmpty || !mirrorable.isEmpty else { return Outcome(toNotify: pending, mirroredIDs: []) }

        guard await CalendarStore.shared.ensureAccess() else { return Outcome(toNotify: pending, mirroredIDs: []) }

        // 「还在 pending」就保留事件（过期但没 ack 的也留着，日历里能看到今天开过什么会）
        let liveIDs = Set(pending.map(\.id))
        // 分页被截断（total > 本页条数）时不按「不在本页」删除，防误删日历事件
        if total <= pending.count {
            removeStaleEvents(keeping: liveIDs)
        }

        guard permanentFailure == nil else { return Outcome(toNotify: pending, mirroredIDs: []) }

        var mirrored = Set<String>()
        let store = CalendarStore.shared.store
        for (reminder, deadline) in mirrorable {
            // 映射还在、事件也还在 → 已经写过，跳过
            if let eventID = mapping[reminder.id], store.event(withIdentifier: eventID) != nil {
                mirrored.insert(reminder.id)
                continue
            }
            if createEvent(for: reminder, deadline: deadline) {
                mirrored.insert(reminder.id)
            }
        }
        return Outcome(toNotify: pending.filter { !mirrored.contains($0.id) }, mirroredIDs: mirrored)
    }

    // MARK: - 写

    private func createEvent(for reminder: Reminder, deadline: Date) -> Bool {
        let cal = CalendarStore.shared
        do {
            let event = EKEvent(eventStore: cal.store)
            event.calendar = try cal.resolveCalendar(explicit: nil)
            event.title = CalendarCore.eventTitle(reminder.content)
            event.notes = reminder.notificationBody
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

    // MARK: - 映射持久化

    private func loadMapping() {
        guard !mappingLoaded else { return }
        mappingLoaded = true
        guard let data = UserDefaults.standard.data(forKey: mapKey) else { return }
        do {
            mapping = try JSONDecoder().decode([String: String].self, from: data)
        } catch {
            LogBuffer.shared.error("[Calendar] 提醒↔事件映射解码失败，按空表处理：\(error)")
        }
    }

    private func persistMapping() {
        do {
            UserDefaults.standard.set(try JSONEncoder().encode(mapping), forKey: mapKey)
        } catch {
            LogBuffer.shared.error("[Calendar] 提醒↔事件映射保存失败：\(error)")
        }
    }
}
#endif
