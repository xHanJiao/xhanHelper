import Foundation

/// 右栏首页用到的纯格式化逻辑（金额 / 相对时间 / 问候 / 当月起始）。
///
/// 抽成无 UI 依赖的纯函数，方便 `swift test` 覆盖边界（见 `Tests/SidebarHomeCoreTests`）；
/// 其中相对时间原来是 `BookmarkPanelView` 与 `HistoryPanelView` 各自的私有实现，
/// 这里收敛成唯一一份，那两处改为调用本文件。
enum SidebarHomeFormat {

    // MARK: - 金额

    /// 千分位、不保留小数；≥ 10 万改「12.8 万」以免撑破卡片。
    /// 非有限值（NaN / ±inf）返回 `—`，不把脏数字画到界面上。
    static func money(_ value: Double) -> String {
        guard value.isFinite else { return "—" }
        if abs(value) >= 100_000 {
            let wan = value / 10_000
            let text = String(format: "%.1f", wan)
            // 12.0 万 → 12 万
            let trimmed = text.hasSuffix(".0") ? String(text.dropLast(2)) : text
            return "\(trimmed) 万"
        }
        let f = NumberFormatter()
        f.numberStyle = .decimal
        f.maximumFractionDigits = 0
        f.roundingMode = .halfUp
        return "¥" + (f.string(from: NSNumber(value: value)) ?? "\(Int(value))")
    }

    // MARK: - 相对时间

    /// 刚刚 / N 分钟前 / N 小时前 / 昨天 / N 天前 / yyyy/MM/dd。
    /// 6 小时内一律按小时（23:55 与 00:10 只差 15 分钟，说「昨天」反而更糊涂）；
    /// 超过 6 小时才按**日历天**说「昨天」——避免 23:59 与次日 00:01 被算成「昨天 / 今天」的错位。
    static func relativeTime(_ date: Date, now: Date = Date(), calendar: Calendar = .current) -> String {
        let interval = now.timeIntervalSince(date)
        if interval < 60 { return "刚刚" }
        if interval < 3600 { return "\(Int(interval / 60)) 分钟前" }
        if interval < 6 * 3600 { return "\(Int(interval / 3600)) 小时前" }

        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: date),
                                           to: calendar.startOfDay(for: now)).day ?? 0
        if days <= 0 { return "\(Int(interval / 3600)) 小时前" }
        if days == 1 { return "昨天" }
        if days < 7 { return "\(days) 天前" }

        let f = DateFormatter()
        f.locale = Locale(identifier: "zh_CN")
        f.dateFormat = "yyyy/MM/dd"
        return f.string(from: date)
    }

    // MARK: - 问候

    /// 时段问候（按小时取整，5 点前算夜深）。
    static func greeting(hour: Int) -> String {
        switch hour {
        case 5..<11: return "早上好"
        case 11..<13: return "中午好"
        case 13..<18: return "下午好"
        case 18..<23: return "晚上好"
        default: return "夜深了"
        }
    }

    // MARK: - 日期

    /// 当月 1 日（本地日历零点）。账单统计的起始日用它。
    static func monthStart(of date: Date = Date(), calendar: Calendar = .current) -> Date {
        let comps = calendar.dateComponents([.year, .month], from: date)
        return calendar.date(from: comps) ?? date
    }

    /// `yyyy-MM-dd`（账单接口的日期参数格式）。时区必须跟着日历走，否则东八区之外的机器会差一天。
    static func dateParam(_ date: Date, calendar: Calendar = .current) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.calendar = calendar
        f.timeZone = calendar.timeZone
        f.dateFormat = "yyyy-MM-dd"
        return f.string(from: date)
    }
}
