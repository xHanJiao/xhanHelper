import XCTest
@testable import FsCore

/// 等级表的**赋值** —— 改一个数字就能悄悄放宽安全姿态，所以逐个钉住关键项。
/// （映射本身见下面的 `ToolConfirmPolicyTests`。）
final class ToolRiskLevelsTests: XCTestCase {

    /// 这三个是 review 后特意提到 L4 的：写盘 / 注入系统事件，必须红色必弹。
    func testBrowserWriteAndSystemEventToolsAreL4() {
        XCTAssertEqual(ToolRiskLevels.level(for: "browser__agent_browser_download"), 4)
        XCTAssertEqual(ToolRiskLevels.level(for: "browser__agent_browser_cgclick"), 4)
        XCTAssertEqual(ToolRiskLevels.level(for: "browser__agent_browser_drag"), 4)
    }

    /// 日历读是 L2 免确认；建/改/删是 L4 必弹（会同步到手机、删改难回滚）。
    func testCalendarWritesAreL4AndReadsSkip() {
        for tool in ["calendar__create_event", "calendar__update_event", "calendar__delete_event"] {
            XCTAssertEqual(ToolRiskLevels.level(for: tool), 4)
            XCTAssertEqual(
                ToolConfirmPolicy.requirement(level: ToolRiskLevels.level(for: tool), isFileSystemTool: false),
                .hardened)
        }
        for tool in ["calendar__list_events", "calendar__search_events"] {
            XCTAssertEqual(ToolRiskLevels.level(for: tool), 2)
            XCTAssertEqual(
                ToolConfirmPolicy.requirement(level: ToolRiskLevels.level(for: tool), isFileSystemTool: false),
                .none)
        }
    }

    /// 打开网址仍是 L3（免确认）：导航本身不写盘、不注入事件，属已记决策。
    func testBrowserOpenStaysL3() {
        XCTAssertEqual(ToolRiskLevels.level(for: "browser__agent_browser_open"), 3)
        XCTAssertEqual(
            ToolConfirmPolicy.requirement(
                level: ToolRiskLevels.level(for: "browser__agent_browser_open"), isFileSystemTool: false),
            .none)
    }

    /// fs 写是 3，但「fs + 3」= 必确认（见 ToolConfirmPolicy 的 fs 特例）。
    func testFileSystemWritesAreL3AndConfirm() {
        for tool in ["fs__write", "fs__edit"] {
            XCTAssertEqual(ToolRiskLevels.level(for: tool), 3)
            XCTAssertEqual(
                ToolConfirmPolicy.requirement(level: ToolRiskLevels.level(for: tool), isFileSystemTool: true),
                .confirm)
        }
    }

    /// 未登记 = 默认 L2 免确认（第三方 MCP 工具走这条）。
    func testUnregisteredDefaultsToL2AndSkips() {
        XCTAssertEqual(ToolRiskLevels.level(for: "mcp__whatever"), ToolRiskLevels.defaultLevel)
        XCTAssertEqual(ToolRiskLevels.level(for: "mcp__whatever"), 2)
        XCTAssertEqual(
            ToolConfirmPolicy.requirement(
                level: ToolRiskLevels.level(for: "mcp__whatever"), isFileSystemTool: false),
            .none)
    }

    /// 表内每条都必须落在 1...4：写成 5 会让所有走 `>= 4` 的判断静默变形。
    func testAllEntriesWithinKnownRange() {
        for (tool, level) in ToolRiskLevels.byTool {
            XCTAssertTrue((1...4).contains(level), "\(tool) 的等级 \(level) 越界")
        }
    }
}

/// 确认闸门的等级映射 —— 这几条就是「AI 能不能不问就动手」的全部规则。
/// 改等级表（`ToolRiskLevels.byTool`）或本映射前，先让这里给出新预期。
final class ToolConfirmPolicyTests: XCTestCase {

    func testL2DoesNotConfirm() {
        XCTAssertEqual(ToolConfirmPolicy.requirement(level: 2, isFileSystemTool: false), .none)
        XCTAssertEqual(ToolConfirmPolicy.requirement(level: 2, isFileSystemTool: true), .none)
    }

    /// 非 fs 的 L3（打开网址、browser 常规交互）免确认 —— 曾经的「信任档」语义，现为唯一行为。
    func testNonFileSystemL3DoesNotConfirm() {
        XCTAssertEqual(ToolConfirmPolicy.requirement(level: 3, isFileSystemTool: false), .none)
    }

    /// fs 的 3 专指「写文件 / 读未授权路径」，必须确认（后者兼作临时授权入口）。
    func testFileSystemL3Confirms() {
        XCTAssertEqual(ToolConfirmPolicy.requirement(level: 3, isFileSystemTool: true), .confirm)
    }

    /// L4 一律红色必弹：shell 写/执行、browser 写盘与注入系统事件。
    func testL4IsHardened() {
        XCTAssertEqual(ToolConfirmPolicy.requirement(level: 4, isFileSystemTool: false), .hardened)
        XCTAssertEqual(ToolConfirmPolicy.requirement(level: 4, isFileSystemTool: true), .hardened)
    }

    /// 越界等级（L5+）也必须确认，不能因为没登记就静默放行。
    func testUnknownHigherLevelStaysHardened() {
        XCTAssertEqual(ToolConfirmPolicy.requirement(level: 5, isFileSystemTool: false), .hardened)
    }
}
