#if os(macOS)
import Foundation

/// 文档 ↔ 聊天 的接线盒（「问 AI」）。
///
/// 两个方向各一个口子：
/// - 文档 → 聊天：正文工具条点「问 AI」→ `DocumentReaderController` 组好 `DocQuote` → `onAsk`
///   （由 ContentView 注册：把引用挂到当前 tab 的 `ChatViewModel` 上）
/// - 聊天 → 文档：底部窄条点「存为批注」→ `reader`（文档详情页 onAppear 注册 / onDisappear 注销）
///   把 AI 回答预填进批注浮层
///
/// 两侧都是闭包 / weak 引用：文档页关掉、聊天栏不在，都不会互相吊住。
@MainActor
final class DocumentAIBridge {
    static let shared = DocumentAIBridge()

    /// 点「问 AI」（ContentView 注册）
    var onAsk: ((DocQuote) -> Void)?

    /// 当前打开的文档阅读器（DocumentFullView 注册）；可能在别的文档、也可能没开
    weak var reader: DocumentReaderController?

    private init() {}
}
#endif
