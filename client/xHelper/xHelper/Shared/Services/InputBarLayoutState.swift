import Foundation
import Combine

/// 输入栏按钮折叠配置（右键菜单控制，持久化，重启保留）。
/// 单例：主聊天与 PiP 两个 ChatView 实例共享同一配置。
@MainActor
final class InputBarLayoutState: ObservableObject {
    static let shared = InputBarLayoutState()

    private static let key = "xhan_inputbar_collapsed_v2"
    /// 老格式（stringArray，只含被收起的 id）。V2 落盘时删掉，只用于一次性迁移。
    private static let legacyKey = "xhan_inputbar_collapsed"

    @Published var collapsed: Set<InputBarButtonID> {
        didSet { persist() }
    }

    private init() {
        let defaults = UserDefaults.standard
        let stored = defaults.dictionary(forKey: Self.key) as? [String: Bool]
        let legacy = defaults.stringArray(forKey: Self.legacyKey)

        // 每个 id 都单独定值：
        // - V2 map 有记录 → 照用户的选择（「常驻」= false 也算数，不会再被打回）
        // - V2 map 缺项 → 只可能是新加的按钮，取 defaultCollapsed
        // - 只有老数组 → 老格式是完整快照，不在里面 = 用户故意设成常驻
        var restored: Set<InputBarButtonID> = []
        for id in InputBarButtonID.allCases {
            let isCollapsed: Bool
            if let stored {
                isCollapsed = stored[id.rawValue] ?? InputBarButtonID.defaultCollapsed.contains(id)
            } else if let legacy {
                isCollapsed = legacy.contains(id.rawValue)
            } else {
                isCollapsed = InputBarButtonID.defaultCollapsed.contains(id)
            }
            if isCollapsed { restored.insert(id) }
        }
        collapsed = restored
        // init 里赋值不触发 didSet：补全结果和迁移结果都得自己落盘
        persist()
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

    /// 按 allCases 写完整 map：显式的 false 也是用户的选择，必须留下来。
    private func persist() {
        let map = Dictionary(uniqueKeysWithValues: InputBarButtonID.allCases.map {
            ($0.rawValue, collapsed.contains($0))
        })
        let defaults = UserDefaults.standard
        defaults.set(map, forKey: Self.key)
        defaults.removeObject(forKey: Self.legacyKey)
    }
}
