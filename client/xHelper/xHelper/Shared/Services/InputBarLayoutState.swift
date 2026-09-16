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
        collapsed = Set(stored.compactMap(InputBarButtonID.init(rawValue:)))
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
