import Foundation

/// 输入栏当前状态快照。由 InputBar 计算后显式传给每个注入的按钮，
/// 按钮据此决定自己的可用性/外观。显式传参（而非 Environment），
/// 便于测试与追踪数据流。
struct InputBarContext {
    /// 是否正在流式输出。
    var isStreaming: Bool
    /// 是否存在可发送的非空内容。
    var canSend: Bool
}

/// 输入栏可折叠按钮的身份。allCases 顺序即规范展示顺序；
/// 每个 id 可被用户设为「常驻」（底部行）或「收起」（更多操作行），
/// 配置持久化在 InputBarLayoutState。发送/停止不在此列（位置固定）。
///
/// **下架按钮必须从 `allCases` 移除**（只改 pill 的 `visibility: .hidden` 不够）：
/// ChatView 的两个 ForEach 按 `allCases` 枚举，留在列表里会在「常驻」行留一个
/// 看不见、也点不到（无命中区 → 右键菜单够不着）的空占位。
/// 恢复按钮 = 加回 `allCases` + 把 pill 的 `visibility` 改回 `.always`。
enum InputBarButtonID: String, CaseIterable {
    case mode
    case agent
    case model
    case brief
    case ephemeral
    case handoff
    case attach

    /// 展示顺序。无痕只有 macOS 有入口（iOS 客户端无无痕功能），故 iOS 上不出现。
    /// 2026-10 下架：model / brief / handoff（三颗 pill 已 hidden）。
    static var allCases: [InputBarButtonID] {
        #if os(macOS)
        return [.mode, .agent, .ephemeral, .attach]
        #else
        return [.mode, .agent, .attach]
        #endif
    }

    /// 首次运行（无持久化配置）时的收起集合，等价于旧硬编码布局。
    /// 无痕（ephemeral）与附件是低频开关，默认收起。
    static let defaultCollapsed: Set<InputBarButtonID> = [.ephemeral, .attach]
}

/// 按钮可见性声明。每个按钮自己决定何时出现/隐藏，
/// 由 InputBar 统一在渲染时过滤（不可见即不占布局）。
enum ButtonVisibility {
    /// 始终显示。
    case always
    /// 完全不显示（暂缓某个按钮时用，不用删代码）。
    case hidden
    /// 自定义规则。
    case predicate((InputBarContext) -> Bool)

    func isVisible(in ctx: InputBarContext) -> Bool {
        switch self {
        case .always: return true
        case .hidden: return false
        case .predicate(let p): return p(ctx)
        }
    }
}
