#if os(macOS)
import Foundation
import EventKit

/// 本机日历的唯一入口（`EKEventStore` 单例 + 授权 + 目标日历挑选 + 事件文本化）。
/// 日历工具（`CalendarToolProvider`）和提醒镜像（`ReminderCalendarMirror`）共用这里，
/// 免得两处各写一套授权/挑选，出现「工具能写、镜像写不进」的不一致。
@MainActor
final class CalendarStore {

    static let shared = CalendarStore()

    struct Failure: Error {
        let message: String
        /// 永久性失败（本机没有能同步的日历 / 指定日历不可用）→ 调用方可以据此本会话不再重试
        var permanent: Bool = false
    }

    let store = EKEventStore()
    /// 拒绝过就不再反复请求（系统框只在 notDetermined 弹一次，反复 request 只会刷日志）
    private var accessDenied = false

    // MARK: - 授权

    /// 有完整访问返回 true；未决定则弹一次系统框；拒绝/受限返回 false。
    func ensureAccess() async -> Bool {
        switch EKEventStore.authorizationStatus(for: .event) {
        case .fullAccess:
            return true
        case .writeOnly:
            // 只写不能读：日历工具要能读，视为未授权
            return false
        case .notDetermined:
            do {
                let granted = try await store.requestFullAccessToEvents()
                if !granted {
                    accessDenied = true
                    LogBuffer.shared.warning("[Calendar] 用户拒绝了日历访问")
                }
                return granted
            } catch {
                LogBuffer.shared.error("[Calendar] requestFullAccessToEvents: \(error)")
                return false
            }
        default:
            if !accessDenied {
                accessDenied = true
                LogBuffer.shared.warning("[Calendar] 日历访问被拒/受限，本会话不再请求")
            }
            return false
        }
    }

    var statusText: String {
        switch EKEventStore.authorizationStatus(for: .event) {
        case .fullAccess: return "已授权"
        case .writeOnly: return "仅写入（读取被拒）"
        case .denied: return "已拒绝"
        case .restricted: return "受系统限制"
        case .notDetermined: return "尚未授权"
        @unknown default: return "未知"
        }
    }

    // MARK: - 日历候选 / 挑选

    private static func sourceTypeName(_ t: EKSourceType) -> String {
        switch t {
        case .local: return "local"
        case .exchange: return "exchange"
        case .calDAV: return "calDAV"
        case .mobileMe: return "mobileMe"
        case .subscribed: return "subscribed"
        case .birthdays: return "birthdays"
        @unknown default: return "unknown"
        }
    }

    private var candidates: [CalendarCore.Candidate] {
        let defaultID = store.defaultCalendarForNewEvents?.calendarIdentifier
        return store.calendars(for: .event).map { cal in
            CalendarCore.Candidate(
                id: cal.calendarIdentifier,
                title: cal.title,
                accountTitle: cal.source?.title ?? "",
                sourceType: Self.sourceTypeName(cal.source?.sourceType ?? .local),
                allowsModification: cal.allowsContentModifications,
                isDefault: cal.calendarIdentifier == defaultID
            )
        }
    }

    /// 可写日历名册（报错文案 / 工具描述用）。
    var writableCalendarNames: [String] {
        CalendarCore.display(candidates.filter { $0.allowsModification && CalendarCore.isSyncable($0) })
    }

    /// 按 `CalendarCore.pick` 的规则取目标日历；取不到抛带人话的 Failure。
    func resolveCalendar(explicit: String?) throws -> EKCalendar {
        switch CalendarCore.pick(from: candidates, explicitName: explicit) {
        case .ok(let id):
            guard let cal = store.calendar(withIdentifier: id) else {
                throw Failure(message: "目标日历已不存在，请重新查看可用日历")
            }
            return cal
        case .explicitUnavailable(let available):
            throw Failure(message: "找不到可写入的日历「\(explicit ?? "")」。可同步到手机的可写日历：\(available.isEmpty ? "无" : available.joined(separator: "、"))", permanent: true)
        case .noSyncable(let available):
            let list = available.isEmpty ? "无" : available.joined(separator: "、")
            throw Failure(message: "本机没有能同步到手机的日历（本地/订阅日历不会同步）。现有可写日历：\(list)", permanent: true)
        }
    }

    // MARK: - 事件文本化

    static let dayFormatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    static let timeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "HH:mm"
        return f
    }()

    /// 一行一个事件：`2026-10-30 14:00-15:00 周会 @工作 id=<identifier>`
    func describe(_ e: EKEvent) -> String {
        let day = Self.dayFormatter.string(from: e.startDate)
        let span: String
        if e.isAllDay {
            span = "全天"
        } else {
            span = "\(Self.timeFormatter.string(from: e.startDate))-\(Self.timeFormatter.string(from: e.endDate))"
        }
        var line = "\(day) \(span) \(e.title ?? "(无标题)") @\(e.calendar?.title ?? "")"
        if let location = e.location, !location.isEmpty { line += " 地点:\(location)" }
        line += " id=\(e.eventIdentifier ?? "")"
        return line
    }
}
#endif
