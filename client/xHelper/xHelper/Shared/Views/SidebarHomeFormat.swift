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

    // MARK: - 到期

    /// 是否已过期（deadline 已过去）。解析失败按「没过期」处理，不误报警。
    static func isOverdue(_ deadline: String, now: Date = Date()) -> Bool {
        guard let date = parseISO(deadline) else { return false }
        return date < now
    }

    /// 到期文案（不含「到期/已过期」前缀，那层语义由调用方的标签/标题承担）：
    /// `今天 18:00` / `明天 09:00` / `11月20日 09:00` / 过期则是 `昨天 05:43`。
    /// 解析失败返回 nil——宁可不显示页脚，也不要瞎猜一个时间。
    static func dueText(_ deadline: String, now: Date = Date(), calendar: Calendar = .current) -> String? {
        guard let date = parseISO(deadline) else { return nil }

        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: now),
                                           to: calendar.startOfDay(for: date)).day ?? 0
        let clock = clockText(date, calendar: calendar)
        let datePart: String
        switch days {
        case 0:  datePart = "今天"
        case 1:  datePart = "明天"
        case 2:  datePart = "后天"
        case -1: datePart = "昨天"
        default: datePart = dayText(date, now: now, calendar: calendar)
        }
        return "\(datePart) \(clock)"
    }

    /// 解析 ISO8601（带/不带小数秒、`Z`/`+08:00`）。与 `LocalArtifacts/LocalArtifactTime.parse` 同口径，
    /// 但本文件要能单独编进 SwiftPM 纯逻辑靶，不能跨模块引用它的内部符号。
    static func parseISO(_ s: String) -> Date? {
        if let d = isoFractionalFormatter.date(from: s) { return d }
        return isoPlainFormatter.date(from: s)
    }

    private static let isoFractionalFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let isoPlainFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    /// `18:00`（24 小时制，跟界面其它时间一致）。
    private static func clockText(_ date: Date, calendar: Calendar) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.calendar = calendar
        f.timeZone = calendar.timeZone
        f.dateFormat = "HH:mm"
        return f.string(from: date)
    }

    /// 同年 `11月20日`，跨年带年份。
    private static func dayText(_ date: Date, now: Date, calendar: Calendar) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "zh_CN")
        f.calendar = calendar
        f.timeZone = calendar.timeZone
        let sameYear = calendar.component(.year, from: date) == calendar.component(.year, from: now)
        f.dateFormat = sameYear ? "M月d日" : "yyyy年M月d日"
        return f.string(from: date)
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

    // MARK: - 首页卡片档位

    /// 首页内容块最大宽（= 654 × 2 + 12 间距）。超过它只加两侧留白，卡片不再拉长。
    static let homeContentMaxWidth: CGFloat = 1320

    /// 按内容宽算列数与档位。纯函数，见 `Tests/SidebarHomeCoreTests`。
    ///
    /// 两列只在**每列都够「三行档」的 340** 时启用（2 × 340 + 12 = 692）：
    /// 门槛还是 620 的话，621 宽会立刻掉成两张 304 的窄卡，直接触发图标档，反而比单列信息更少。
    static func homeMetrics(contentWidth: CGFloat) -> HomeCardMetrics {
        let width = max(0, contentWidth)

        // 最窄：两张卡都放不下文字，退成 2 列纯图标（单列一张空壳比两个方块更难看）
        if width < 340 {
            // 极端测量态（宽 < 12）也别算出负边长
            let side = max(0, (width - 12) / 2)
            return HomeCardMetrics(layout: .iconOnly, columns: 2, cardWidth: side, cardHeight: side)
        }

        if width < 2 * 340 + 12 {
            let layout = tier(cardWidth: width)
            return HomeCardMetrics(layout: layout, columns: 1, cardWidth: width,
                                   cardHeight: layout == .info ? 164 : 104)
        }

        let cardWidth = (width - 12) / 2
        let layout = tier(cardWidth: cardWidth)
        return HomeCardMetrics(layout: layout, columns: 2, cardWidth: cardWidth,
                               cardHeight: layout == .info ? 164 : 132)
    }

    private static func tier(cardWidth: CGFloat) -> HomeCardLayout {
        if cardWidth >= 560 { return .info }
        if cardWidth >= 440 { return .singleLine }
        return .stacked
    }

    // MARK: - 信息行图标

    /// 账单一级分类 → SF Symbol。`category1` 是后端固定 12 项枚举（见 `model/bill.go`）；
    /// 未知分类返回 nil，由调用方退回首字色块——不猜、不新造字段。
    static func billIcon(category1: String) -> String? {
        switch category1 {
        case "餐饮": return "fork.knife"
        case "服装": return "tshirt"
        case "交通": return "tram"
        case "居住": return "house"
        case "旅游": return "airplane"
        case "美容": return "scissors"
        case "人情": return "gift"
        case "日用": return "basket"
        case "数码": return "laptopcomputer"
        case "学习": return "book"
        case "医疗": return "cross.case"
        case "娱乐": return "gamecontroller"
        default: return nil
        }
    }

    /// 首字色块用的一个字（中文取首字，英文取首字母大写）。空串退回 `·`，不留空白 chip。
    static func monogram(_ title: String) -> String {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = trimmed.first else { return "·" }
        return String(first).uppercased()
    }
}

/// 入口卡四档：信息（插画 + 3 条可点行 + 文字）/ 单行 + 细节 / 三行文字 / 纯图标。
enum HomeCardLayout: Equatable {
    case info
    case singleLine
    case stacked
    case iconOnly
}

struct HomeCardMetrics: Equatable {
    let layout: HomeCardLayout
    let columns: Int
    let cardWidth: CGFloat
    let cardHeight: CGFloat
}
