import Foundation

/// 工具风险等级表 + 「等级 → 要不要弹确认条」的映射。
///
/// 两者放同一个文件、一起镜像进 SwiftPM 的 FsCore target（单测 `Tests/FsCoreTests/ToolConfirmPolicyTests.swift`），
/// 因为它们合起来就是「AI 能不能不问就动手」的唯一契约：
///   - `ToolRiskLevels`：纯数据（哪类工具算几级），`LocalToolRegistry` 读它。
///   - `ToolConfirmPolicy`：等级 → 是否确认。
///
/// 改这里的任何一条都会改变安全姿态 —— 改前先让单测给出新预期。
nonisolated enum ToolRiskLevels {

    /// flatName（`namespace__function`）→ 风险等级；未登记按 `defaultLevel`。
    ///
    /// - L1 / L2：只读 / 低风险 → 免确认。
    /// - L3：**一般风险，默认免确认**（如打开网址）。唯一例外是 fs 工具的 L3 —— `LocalToolRegistry`
    ///   的 fs 分支把「写文件」与「读未授权路径」都算成 3 → 必须确认。
    /// - L4：**高危，必弹红色确认条**。
    ///
    /// 所以：**新工具「本意要确认」就给它 L4** —— L3 是免确认的，没有启动期校验会替你兜（但本表有单测）。
    /// shell / shell_peek 不在表内：它们按命令动态定级（只读 2 / 写·执行 4）。
    /// 等级仅供客户端执行控制，不随 schema 下发给 LLM。
    static let byTool: [String: Int] = [
        // L1 只读
        "browser__agent_browser_snapshot": 1,
        "browser__agent_browser_read": 1,
        "browser__agent_browser_get_text": 1,
        "browser__agent_browser_get_value": 1,
        "browser__agent_browser_get_url": 1,
        "browser__agent_browser_get_title": 1,
        "browser__agent_browser_list_tabs": 1,
        "browser__agent_browser_wait_for_load": 1,
        "browser__agent_browser_wait_for_selector": 1,
        // L3 一般风险（免确认）：打开任意 URL（可能导航到任意站点）
        "browser__agent_browser_open": 3,
        // L4 高危（红色必弹）：下载文件到本地 —— 与 fs 写同属「往磁盘落文件」，必须确认
        "browser__agent_browser_download": 4,
        // L4 高危（红色必弹）：真实系统级点击/拖拽 —— 注入系统事件，可能打断用户操作/点错目标
        "browser__agent_browser_cgclick": 4,
        "browser__agent_browser_drag": 4,
        // L3 需确认（fs 分支特例）：本地文件写入（覆盖/追加/编辑，破坏性操作）
        "fs__write": 3,
        "fs__edit": 3,
        // L2 只读（显式登记：默认等级一旦调整，不至于把日历读连带变成必弹）
        "calendar__list_events": 2,
        "calendar__search_events": 2,
        // L4 高危（红色必弹）：往用户日历里写/改/删事件 —— 会同步到手机，且删改难以回滚
        "calendar__create_event": 4,
        "calendar__update_event": 4,
        "calendar__delete_event": 4,
    ]

    /// 未登记工具的等级（低风险写）。改它会一次性影响所有第三方 MCP 工具。
    static let defaultLevel = 2

    static func level(for flatName: String) -> Int {
        byTool[flatName] ?? defaultLevel
    }
}

/// 「等级 → 要不要弹确认条」的唯一映射。
///
/// 语义（改 `ToolRiskLevels` 前先读这段）：
/// - L1 / L2：免确认，直接执行。
/// - L3：**一般风险，默认免确认**（打开网址、浏览器交互等）。唯一例外是 fs 工具的 L3——registry 的
///   fs 分支把「写文件」和「读未授权路径」都算成 3（后者兼作「批准后临时授权该路径」的入口），必须确认。
/// - L4：**高危，必弹红色确认条**，且拿到红色 UI「请求执行高危操作」。当前有：shell 写/执行、
///   browser 的写盘（download）与注入系统事件（cgclick / drag）、日历的建/改/删（会同步到手机）。
///
/// 两个运行时开关（都只影响确认条，不影响硬拦截：命令白名单 / 高危路径 / 敏感文件名 / 未授权目录）：
/// - `forcedL3`：敏感上下文动态升级（高敏域 / http 明文页 / 用户标签写入授权），**任何情况都必须确认**
///   （防注入最后一道），完全信任也不跳过。
/// - `fullTrust`：全局「完全信任」，跳过上面所有静态等级的确认。**只对已授权目录生效**：fs 的 L3
///   （写文件 / 读未授权路径）原本靠确认条顺带临时授权路径 1h，全信任下不再授权——范围外目录由调用点
///   直接报错（见 `ChatViewModel+ClientTools` 的 fs 分支）。
nonisolated enum ToolConfirmPolicy {

    enum Requirement {
        /// 免确认，直接执行。
        case none
        /// 弹常规确认条。
        case confirm
        /// 弹红色确认条（`ToolConfirmation.hardened`）。
        case hardened
    }

    static func requirement(
        level: Int,
        isFileSystemTool: Bool,
        forcedL3: Bool = false,
        fullTrust: Bool = false
    ) -> Requirement {
        if forcedL3 { return level >= 4 ? .hardened : .confirm }
        if fullTrust { return .none }
        if level >= 4 { return .hardened }
        if level == 3, isFileSystemTool { return .confirm }
        return .none
    }

    /// fs 的 L3（写文件 / 读未授权路径）落到未授权目录时怎么办。
    ///
    /// 确认条原本还有第二个作用：用户点了「允许」就**临时授权该路径 1h**。所以完全信任只能免掉确认，
    /// 不能顺手把授权也免了——否则模型点名的任意路径都会被静默放行，实际权限超过开启时承诺的范围。
    /// 与 shell 一致：范围外目录一律拒，让用户自己去「设置 → 文件访问授权」添加（一次性、永久）。
    enum FsUnauthorizedAction: Equatable {
        /// 拒绝执行（报「未授权目录」，不落盘）。
        case block
        /// 确认条已获用户允许 → 临时授权该路径。
        case authorizeTemporarily
    }

    static func fsUnauthorizedAction(unauthorized: [String], fullTrust: Bool) -> FsUnauthorizedAction {
        unauthorized.isEmpty || !fullTrust ? .authorizeTemporarily : .block
    }
}
