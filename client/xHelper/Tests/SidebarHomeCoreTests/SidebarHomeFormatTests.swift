import XCTest
@testable import SidebarHomeCore

final class SidebarHomeFormatTests: XCTestCase {

    // MARK: - 金额

    func testMoneyThousandsAndNoDecimals() {
        XCTAssertEqual(SidebarHomeFormat.money(0), "¥0")
        XCTAssertEqual(SidebarHomeFormat.money(999), "¥999")
        XCTAssertEqual(SidebarHomeFormat.money(1284), "¥1,284")
        XCTAssertEqual(SidebarHomeFormat.money(1284.6), "¥1,285")
    }

    func testMoneyWanAboveHundredThousand() {
        XCTAssertEqual(SidebarHomeFormat.money(100_000), "10 万")
        XCTAssertEqual(SidebarHomeFormat.money(128_400), "12.8 万")
        XCTAssertEqual(SidebarHomeFormat.money(-128_400), "-12.8 万")
    }

    func testMoneyNonFiniteShowsDash() {
        XCTAssertEqual(SidebarHomeFormat.money(.nan), "—")
        XCTAssertEqual(SidebarHomeFormat.money(.infinity), "—")
    }

    // MARK: - 相对时间

    private func date(_ string: String) -> Date {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "Asia/Shanghai")
        f.dateFormat = "yyyy-MM-dd HH:mm:ss"
        return f.date(from: string)!
    }

    private var calendar: Calendar {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "Asia/Shanghai")!
        return c
    }

    func testRelativeTimeWithinHour() {
        let now = date("2026-11-12 10:00:00")
        XCTAssertEqual(SidebarHomeFormat.relativeTime(date("2026-11-12 09:59:40"), now: now, calendar: calendar), "刚刚")
        XCTAssertEqual(SidebarHomeFormat.relativeTime(date("2026-11-12 09:30:00"), now: now, calendar: calendar), "30 分钟前")
    }

    func testRelativeTimeYesterdayUsesCalendarDay() {
        let now = date("2026-11-12 00:10:00")
        // 只差 15 分钟：说「15 分钟前」比「昨天」好懂
        XCTAssertEqual(SidebarHomeFormat.relativeTime(date("2026-11-11 23:55:00"), now: now, calendar: calendar), "15 分钟前")
        // 2 小时前仍是小时
        XCTAssertEqual(SidebarHomeFormat.relativeTime(date("2026-11-11 22:00:00"), now: now, calendar: calendar), "2 小时前")
        // 超过 6 小时且跨天 → 昨天
        XCTAssertEqual(SidebarHomeFormat.relativeTime(date("2026-11-11 18:00:00"), now: now, calendar: calendar), "昨天")
    }

    func testRelativeTimeDaysAndAbsoluteDate() {
        let now = date("2026-11-12 10:00:00")
        XCTAssertEqual(SidebarHomeFormat.relativeTime(date("2026-11-09 10:00:00"), now: now, calendar: calendar), "3 天前")
        XCTAssertEqual(SidebarHomeFormat.relativeTime(date("2026-10-01 10:00:00"), now: now, calendar: calendar), "2026/10/01")
    }

    // MARK: - 到期

    func testDueTextTodayTomorrowAndClock() {
        let now = date("2026-11-12 10:00:00")
        XCTAssertEqual(SidebarHomeFormat.dueText("2026-11-12T18:00:00+08:00", now: now, calendar: calendar), "今天 18:00")
        XCTAssertEqual(SidebarHomeFormat.dueText("2026-11-13T09:30:00+08:00", now: now, calendar: calendar), "明天 09:30")
        XCTAssertEqual(SidebarHomeFormat.dueText("2026-11-14T00:05:00+08:00", now: now, calendar: calendar), "后天 00:05")
    }

    func testDueTextOverdueAndFarDates() {
        let now = date("2026-11-12 10:00:00")
        // 跨天但只差 10 分钟：按日历天说「昨天」，时间照实给
        XCTAssertEqual(SidebarHomeFormat.dueText("2026-11-11T23:50:00+08:00", now: now, calendar: calendar), "昨天 23:50")
        XCTAssertEqual(SidebarHomeFormat.dueText("2026-11-20T09:00:00+08:00", now: now, calendar: calendar), "11月20日 09:00")
        // 跨年带年份
        XCTAssertEqual(SidebarHomeFormat.dueText("2027-01-11T09:00:00+08:00", now: now, calendar: calendar), "2027年1月11日 09:00")
        // 解析失败不猜时间
        XCTAssertNil(SidebarHomeFormat.dueText("", now: now, calendar: calendar))
        XCTAssertNil(SidebarHomeFormat.dueText("明天下午", now: now, calendar: calendar))
    }

    func testIsOverdue() {
        let now = date("2026-11-12 10:00:00")
        XCTAssertTrue(SidebarHomeFormat.isOverdue("2026-11-12T09:59:00+08:00", now: now))
        XCTAssertFalse(SidebarHomeFormat.isOverdue("2026-11-12T10:01:00+08:00", now: now))
        // 解析失败按「没过期」，不误报橙色标
        XCTAssertFalse(SidebarHomeFormat.isOverdue("", now: now))
    }

    func testDueRelative() {
        let now = date("2026-11-12 10:00:00")
        XCTAssertEqual(SidebarHomeFormat.dueRelative("2026-11-12T20:00:00+08:00", now: now, calendar: calendar), "今天")
        XCTAssertEqual(SidebarHomeFormat.dueRelative("2026-11-13T09:00:00+08:00", now: now, calendar: calendar), "明天")
        XCTAssertEqual(SidebarHomeFormat.dueRelative("2026-11-14T09:00:00+08:00", now: now, calendar: calendar), "后天")
        XCTAssertEqual(SidebarHomeFormat.dueRelative("2026-11-20T09:00:00+08:00", now: now, calendar: calendar), "8 天后")
        // 过期走同一套「n 天前」，跟文档卡的相对时间口径一致
        XCTAssertEqual(SidebarHomeFormat.dueRelative("2026-11-11T09:00:00+08:00", now: now, calendar: calendar), "昨天")
        XCTAssertEqual(SidebarHomeFormat.dueRelative("2026-11-08T09:00:00+08:00", now: now, calendar: calendar), "4 天前")
        // 解析失败不猜时间
        XCTAssertNil(SidebarHomeFormat.dueRelative("", now: now, calendar: calendar))
    }

    // MARK: - 问候与当月起始

    func testGreetingBuckets() {
        XCTAssertEqual(SidebarHomeFormat.greeting(hour: 3), "夜深了")
        XCTAssertEqual(SidebarHomeFormat.greeting(hour: 7), "早上好")
        XCTAssertEqual(SidebarHomeFormat.greeting(hour: 12), "中午好")
        XCTAssertEqual(SidebarHomeFormat.greeting(hour: 15), "下午好")
        XCTAssertEqual(SidebarHomeFormat.greeting(hour: 21), "晚上好")
    }

    func testMonthStartBoundaries() {
        XCTAssertEqual(SidebarHomeFormat.dateParam(
            SidebarHomeFormat.monthStart(of: date("2026-01-31 23:00:00"), calendar: calendar),
            calendar: calendar), "2026-01-01")
        XCTAssertEqual(SidebarHomeFormat.dateParam(
            SidebarHomeFormat.monthStart(of: date("2026-12-01 00:00:00"), calendar: calendar),
            calendar: calendar), "2026-12-01")
    }

    // MARK: - 卡片档位

    func testHomeMetricsSingleColumnTiers() {
        for (width, layout, height) in [(340.0, HomeCardLayout.stacked, 104.0),
                                        (439.0, .stacked, 104.0),
                                        (440.0, .singleLine, 104.0),
                                        (559.0, .singleLine, 104.0),
                                        (560.0, .info, 164.0),
                                        (691.0, .info, 164.0)] as [(CGFloat, HomeCardLayout, CGFloat)] {
            let m = SidebarHomeFormat.homeMetrics(contentWidth: width)
            XCTAssertEqual(m.columns, 1, "宽 \(width) 应为单列")
            XCTAssertEqual(m.layout, layout, "宽 \(width) 档位")
            XCTAssertEqual(m.cardWidth, width)
            XCTAssertEqual(m.cardHeight, height, "宽 \(width) 卡高")
        }
    }

    func testHomeMetricsTwoColumnsOnlyWhenBothFitStacked() {
        // 692 = 2 × 340 + 12：刚够两列都进三行档，不留「621 宽掉成图标」的悬崖
        let edge = SidebarHomeFormat.homeMetrics(contentWidth: 692)
        XCTAssertEqual(edge.columns, 2)
        XCTAssertEqual(edge.layout, .stacked)
        XCTAssertEqual(edge.cardWidth, 340)
        XCTAssertEqual(edge.cardHeight, 132)

        let single = SidebarHomeFormat.homeMetrics(contentWidth: 691)
        XCTAssertEqual(single.columns, 1)
        XCTAssertEqual(single.layout, .info)
    }

    func testHomeMetricsTwoColumnTiers() {
        let singleLine = SidebarHomeFormat.homeMetrics(contentWidth: 900)   // 卡 444
        XCTAssertEqual(singleLine.columns, 2)
        XCTAssertEqual(singleLine.layout, .singleLine)
        XCTAssertEqual(singleLine.cardHeight, 132)

        let info = SidebarHomeFormat.homeMetrics(contentWidth: 1132)        // 卡 560
        XCTAssertEqual(info.columns, 2)
        XCTAssertEqual(info.layout, .info)
        XCTAssertEqual(info.cardHeight, 164)
    }

    func testHomeMetricsNarrowestBecomesTwoColumnsOfIcons() {
        let m = SidebarHomeFormat.homeMetrics(contentWidth: 328)            // 右栏 380
        XCTAssertEqual(m.columns, 2)
        XCTAssertEqual(m.layout, .iconOnly)
        XCTAssertEqual(m.cardWidth, 158)
        XCTAssertEqual(m.cardHeight, 158)                                  // 方块：高 = 宽
    }

    func testHomeMetricsAtContentCap() {
        let m = SidebarHomeFormat.homeMetrics(contentWidth: SidebarHomeFormat.homeContentMaxWidth)
        XCTAssertEqual(SidebarHomeFormat.homeContentMaxWidth, 1320)
        XCTAssertEqual(m.columns, 2)
        XCTAssertEqual(m.cardWidth, 654)
        XCTAssertEqual(m.layout, .info)
    }

    func testHomeMetricsIconBoundaryAt340() {
        let narrow = SidebarHomeFormat.homeMetrics(contentWidth: 339)
        XCTAssertEqual(narrow.layout, .iconOnly)
        XCTAssertEqual(narrow.columns, 2)

        let stacked = SidebarHomeFormat.homeMetrics(contentWidth: 340)
        XCTAssertEqual(stacked.layout, .stacked)
        XCTAssertEqual(stacked.columns, 1)

        // 极端测量态（宽 < 12）不产出负边长
        XCTAssertEqual(SidebarHomeFormat.homeMetrics(contentWidth: 0).cardWidth, 0)
        XCTAssertEqual(SidebarHomeFormat.homeMetrics(contentWidth: 0).cardHeight, 0)
    }

    // MARK: - 信息行图标

    func testBillIconCoversEveryCategory() {
        // 后端固定 12 项枚举（server/internal/model/bill.go）
        let categories = ["餐饮", "服装", "交通", "居住", "旅游", "美容",
                          "人情", "日用", "数码", "学习", "医疗", "娱乐"]
        for category in categories {
            XCTAssertNotNil(SidebarHomeFormat.billIcon(category1: category), "分类「\(category)」缺图标")
        }
        // 未知分类交给首字色块兜底，不瞎映射
        XCTAssertNil(SidebarHomeFormat.billIcon(category1: ""))
        XCTAssertNil(SidebarHomeFormat.billIcon(category1: "其他"))
    }

    func testMonogram() {
        XCTAssertEqual(SidebarHomeFormat.monogram("周报草稿"), "周")
        XCTAssertEqual(SidebarHomeFormat.monogram("xhanHelper 设计备忘"), "X")
        XCTAssertEqual(SidebarHomeFormat.monogram("  20261108 · 充实的一天"), "2")
        XCTAssertEqual(SidebarHomeFormat.monogram(""), "·")
        XCTAssertEqual(SidebarHomeFormat.monogram("   "), "·")
    }
}
