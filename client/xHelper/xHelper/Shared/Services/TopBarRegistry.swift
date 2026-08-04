import Foundation
import Combine
import SwiftUI

/// 顶栏槽位：左/中/右
enum TopBarSlot: Hashable {
    case left, center, right
}

/// 顶栏按钮定义（closure 动态求值：每次渲染取最新状态）
struct TopBarButton: Identifiable {
    let id: String
    let icon: () -> String
    let enabled: () -> Bool
    let action: () -> Void
    let help: () -> String
    let isRefresh: Bool
    /// 键盘快捷键（挂到渲染的按钮上；未渲染时自动注销）
    let keyEquivalent: (key: KeyEquivalent, modifiers: EventModifiers)?

    init(id: String, icon: @escaping () -> String, enabled: @escaping () -> Bool,
         action: @escaping () -> Void, help: @escaping () -> String, isRefresh: Bool = false,
         keyEquivalent: (key: KeyEquivalent, modifiers: EventModifiers)? = nil) {
        self.id = id
        self.icon = icon
        self.enabled = enabled
        self.action = action
        self.help = help
        self.isRefresh = isRefresh
        self.keyEquivalent = keyEquivalent
    }
}

/// 顶栏按钮组：归属于某槽位，带优先级（越高越优先；同最高优先级多组共存）
struct TopBarGroup: Identifiable {
    let id: String
    let slot: TopBarSlot
    let priority: Int
    let buttons: [TopBarButton]
}

/// 通用顶栏注册中心（@MainActor 单例）。
/// 各功能模块向对应槽位注册/注销自己的按钮组；渲染时每槽位取最高优先级的组（同优先多组共存）。
@MainActor
final class TopBarRegistry: ObservableObject {
    static let shared = TopBarRegistry()

    private var groups: [TopBarSlot: [TopBarGroup]] = [:]
    @Published private(set) var version: Int = 0

    private init() {}

    /// 注册（同 id + slot 覆盖更新）
    func register(_ group: TopBarGroup) {
        var slotGroups = groups[group.slot] ?? []
        slotGroups.removeAll { $0.id == group.id }
        slotGroups.append(group)
        groups[group.slot] = slotGroups
        bump()
    }

    /// 注销（按 id，跨槽位）
    func unregister(id: String) {
        var changed = false
        for slot in groups.keys {
            let before = groups[slot]?.count ?? 0
            groups[slot]?.removeAll { $0.id == id }
            if (groups[slot]?.count ?? 0) != before { changed = true }
        }
        if changed { bump() }
    }

    /// 注销（按 id 前缀，批量）
    func unregister(prefix: String) {
        var changed = false
        for slot in groups.keys {
            let before = groups[slot]?.count ?? 0
            groups[slot]?.removeAll { $0.id.hasPrefix(prefix) }
            if (groups[slot]?.count ?? 0) != before { changed = true }
        }
        if changed { bump() }
    }

    /// 某槽位可见的组：取最高优先级，同优先多组共存（按注册顺序）
    func visibleGroups(for slot: TopBarSlot) -> [TopBarGroup] {
        let slotGroups = groups[slot] ?? []
        guard !slotGroups.isEmpty else { return [] }
        let maxPriority = slotGroups.map(\.priority).max() ?? 0
        return slotGroups.filter { $0.priority == maxPriority }
    }

    private func bump() {
        version += 1
    }
}
