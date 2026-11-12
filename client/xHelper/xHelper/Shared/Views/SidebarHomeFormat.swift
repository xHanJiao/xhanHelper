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

    /// 相对到期说法，跟其它卡右侧的「3 天前」同一套口径：
    /// 未来 `今天 / 明天 / 后天 / N 天后`，过期 `今天 / 昨天 / N 天前`。
    /// 卡头要具体时刻用 `dueText`；卡片信息行只放相对距离，免得把标题挤没。
    static func dueRelative(_ deadline: String, now: Date = Date(), calendar: Calendar = .current) -> String? {
        guard let date = parseISO(deadline) else { return nil }
        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: now),
                                           to: calendar.startOfDay(for: date)).day ?? 0
        switch days {
        case 0:  return "今天"
        case 1:  return "明天"
        case 2:  return "后天"
        case -1: return "昨天"
        default: return days > 0 ? "\(days) 天后" : "\(-days) 天前"
        }
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

    /// 首页纵向除卡片外的固定开销（问候行 ~47 + 卡片上间距 16 + 行间 2×12 + 上下 padding 36）。
    /// 近似值，只用来判断「三行卡放不放得下」，差几个点不影响结论。
    static let homeChromeHeight: CGFloat = 123

    /// 按内容宽 + 可用高算列数与档位。纯函数，见 `Tests/SidebarHomeCoreTests`。
    ///
    /// **永远两列**（用户 2026-11-12 拍板）：内容块窄了就靠档位降级（三行 → 单行 → 图标卡），
    /// 不再退回单列——单列时一行只有一张卡，看着像列表而不是入口宫格。
    ///
    /// `availableHeight` 传视图实际可用高（不传 = 不考虑高度，老调用点行为不变）：
    /// 三行 164 的信息档放不下时降到 132 的单行档；连 132 都放不下就**允许整页滚动**，
    /// 不再继续压——卡高压到 100 出头文字和插画都会糊。
    static func homeMetrics(contentWidth: CGFloat, availableHeight: CGFloat = .infinity) -> HomeCardMetrics {
        let width = max(0, contentWidth)
        // 极端测量态（宽 < 12）也别算出负边长
        let cardWidth = max(0, (width - 12) / 2)
        // 每张卡最多能有高；窗口极矮时允许为负，后面 clamp 掉
        let maxCardHeight = availableHeight.isFinite ? (availableHeight - homeChromeHeight) / 3 : .infinity

        // 卡宽放不下文字才退成纯图标卡：200 以下缩完插画带文字区只剩 ~40pt，
        // 「本月 ¥2,963」会被截成「本月 ¥…」，不如干净给插画（用户 2026-11-12 拍板）。
        // 两列也不拆，拆了就是单列。
        // 高度封顶 170（不封顶时卡宽 300 上下会变成一块 300×300 的巨型方块），下限 110
        // （再矮就整页滚，别把插画压成一条扁带）。
        if cardWidth < 200 {
            return HomeCardMetrics(layout: .iconOnly, columns: 2, cardWidth: cardWidth,
                                   cardHeight: max(110, min(cardWidth, 170, maxCardHeight)))
        }

        // 只有信息档比别的档高（164 vs 132），所以高度不够时降这一档就够；132 是底线。
        let base = tier(cardWidth: cardWidth)
        let layout = base == .info && maxCardHeight < 164 ? .singleLine : base
        return HomeCardMetrics(layout: layout, columns: 2, cardWidth: cardWidth,
                               cardHeight: layout == .info ? 164 : 132)
    }

    /// 三行档里副文案（第三行）的最低卡宽。窄于此只留「标题 / 主数字」两行——
    /// 200 出头的卡里第三行只会被截成「上次停在 Dee…」，不如不显示。
    static let homeSecondaryMinCardWidth: CGFloat = 300

    /// 插画带宽度：跟着卡宽缩（0.42，最多 = 卡高）。
    /// 卡宽 440 以上恒为卡高（原来的口径）；300 的卡带 126，200 的卡带 84，把横向空间让给文字。
    static func homeBandWidth(cardWidth: CGFloat, cardHeight: CGFloat) -> CGFloat {
        max(0, min(cardHeight, cardWidth * 0.42))
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
