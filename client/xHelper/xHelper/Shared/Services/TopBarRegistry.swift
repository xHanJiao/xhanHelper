import Foundation
import Combine
import SwiftUI

/// 顶栏槽位：中/右（左区为顶栏 leading View，不走槽位系统）
enum TopBarSlot: Hashable {
    case center, right
}

/// 顶栏按钮定义（closure 动态求值：每次渲染取最新状态）
struct TopBarButton: Identifiable {
    let id: String
    let icon: () -> String
    let enabled: () -> Bool
    /// 是否渲染（false 时整个按钮不显示；默认恒显示，仅 enabled 置灰）
    let visible: () -> Bool
    let action: () -> Void
    let help: () -> String
    let isRefresh: Bool
    /// 键盘快捷键（挂到渲染的按钮上；未渲染时自动注销）
    let keyEquivalent: (key: KeyEquivalent, modifiers: EventModifiers)?

    init(id: String, icon: @escaping () -> String, enabled: @escaping () -> Bool,
         action: @escaping () -> Void, help: @escaping () -> String, isRefresh: Bool = false,
         visible: @escaping () -> Bool = { true },
         keyEquivalent: (key: KeyEquivalent, modifiers: EventModifiers)? = nil) {
        self.id = id
        self.icon = icon
        self.enabled = enabled
        self.visible = visible
        self.action = action
        self.help = help
        self.isRefresh = isRefresh
        self.keyEquivalent = keyEquivalent
    }
}

/// 顶栏按钮组：归属于某槽位，带优先级（越高越优先；同最高优先级多组共存）。
/// `visible` 闭包在渲染时求值决定整组显隐（与按钮级 visible 对齐）：
/// 通用契约 = 注册一次 + visible 闭包判自身状态 + 状态变化时调 `TopBarRegistry.refresh()`，
/// 不再需要 register/unregister 来切换显隐。
struct TopBarGroup: Identifiable {
    let id: String
    let slot: TopBarSlot
    let priority: Int
    let buttons: [TopBarButton]
    let visible: () -> Bool
    /// 允许降级时**部分收**：放不下就把这一组的末尾换成 `⋯`（能留几个留几个，点 `⋯` 出菜单）。
    /// 默认 `false` = 只能"整组在 / 整组不在"（右槽导航组与工具组就靠它保证永不变形）。
    /// ⚠️ 今天只有**中心槽**的组会走这条路径（右槽两组的宽在 `TopBarLayout` 里是整体计入的）。
    /// 见 `docs/design/overflow-collapse/design.md`。
    let autoOverflow: Bool

    init(id: String, slot: TopBarSlot, priority: Int, buttons: [TopBarButton],
         visible: @escaping () -> Bool = { true }, autoOverflow: Bool = false) {
        self.id = id
        self.slot = slot
        self.priority = priority
        self.buttons = buttons
        self.visible = visible
        self.autoOverflow = autoOverflow
    }
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

    /// 某槽位可见的组：先过滤 visible() 为 true 的组，再取最高优先级；同优先多组共存（按注册顺序）。
    /// 高优先级组不可见时自动让位给低优先级可见组（如浏览器组隐藏时其他功能区按钮可显示）。
    func visibleGroups(for slot: TopBarSlot) -> [TopBarGroup] {
        let shown = (groups[slot] ?? []).filter { $0.visible() }
        guard !shown.isEmpty else { return [] }
        let maxPriority = shown.map(\.priority).max() ?? 0
        return shown.filter { $0.priority == maxPriority }
    }

    /// 触发顶栏重算：组外部状态（未依附于顶栏观察的对象）变化后调用，让 visible 闭包重新求值。
    /// 组可见状态依附于顶栏已观察的对象（如 RightSidebarManager）时顶栏会自动重算，可不调；统一调用最稳妥。
    func refresh() {
        bump()
    }

    private func bump() {
        version += 1
    }
}
