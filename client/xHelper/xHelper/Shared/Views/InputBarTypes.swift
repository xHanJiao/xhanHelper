import Foundation

/// 输入栏当前状态快照。由 InputBar 计算后显式传给每个注入的按钮，
/// 按钮据此决定自己的可见性/可用性/外观。显式传参（而非 Environment），
/// 便于测试与追踪数据流。
struct InputBarContext {
    /// 是否处于收起态（浏览态）：未聚焦 + 空内容 + 非流式。
    var isCollapsed: Bool
    /// 是否正在流式输出。
    var isStreaming: Bool
    /// 是否存在可发送的非空内容。
    var canSend: Bool
    /// 编辑器是否持有焦点。
    var isFocused: Bool
}

/// 按钮可见性声明。每个按钮自己决定在什么状态下出现/隐藏，
/// 由 InputBar 统一在渲染时过滤（不可见即不占布局）。
enum ButtonVisibility {
    /// 收起态与展开态都显示。
    case always
    /// 仅收起态（浏览态）显示。
    case whenCollapsed
    /// 仅展开态（输入态）显示。
    case whenExpanded
    /// 完全不显示（暂缓某个按钮时用，不用删代码）。
    case hidden
    /// 自定义规则。
    case predicate((InputBarContext) -> Bool)

    func isVisible(in ctx: InputBarContext) -> Bool {
        switch self {
        case .always: return true
        case .whenCollapsed: return ctx.isCollapsed
        case .whenExpanded: return !ctx.isCollapsed
        case .hidden: return false
        case .predicate(let p): return p(ctx)
        }
    }
}
