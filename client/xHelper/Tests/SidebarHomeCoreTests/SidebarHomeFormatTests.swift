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
}
