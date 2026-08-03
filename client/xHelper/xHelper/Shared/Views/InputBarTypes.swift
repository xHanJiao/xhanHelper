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
