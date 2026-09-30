import Foundation
import Combine

/// 输入栏按钮折叠配置（右键菜单控制，持久化，重启保留）。
/// 单例：主聊天与 PiP 两个 ChatView 实例共享同一配置。
@MainActor
final class InputBarLayoutState: ObservableObject {
    static let shared = InputBarLayoutState()

    private static let key = "xhan_inputbar_collapsed"

    @Published var collapsed: Set<InputBarButtonID> {
        didSet { UserDefaults.standard.set(collapsed.map(\.rawValue), forKey: Self.key) }
    }

    private init() {
        guard let stored = UserDefaults.standard.stringArray(forKey: Self.key) else {
            collapsed = InputBarButtonID.defaultCollapsed
            return
        }
        var restored = Set(stored.compactMap(InputBarButtonID.init(rawValue:)))
        // 新增按钮要补进 collapsed：老用户存下来的集合里没这个 id，Set(stored) 会让它落在
        // "未收起"→ 显示在常驻行，与 defaultCollapsed 的意图相反。新 id 不可能被用户主动
        // 展开过，所以"缺失即补入"安全；补进去后用户再展开会被存下来，不会重复补。
        // 以后再加按钮，这里跟着补一行。
        if !stored.contains(InputBarButtonID.brief.rawValue) { restored.insert(.brief) }
        if !stored.contains(InputBarButtonID.ephemeral.rawValue) { restored.insert(.ephemeral) }
        collapsed = restored
    }

    func isCollapsed(_ id: InputBarButtonID) -> Bool {
        collapsed.contains(id)
    }

    func toggle(_ id: InputBarButtonID) {
        if collapsed.contains(id) {
            collapsed.remove(id)
        } else {
            collapsed.insert(id)
        }
    }
}
