import SwiftUI
import Combine

struct ToolbarAction: Identifiable {
    let id: String
    let icon: String
    let action: () -> Void
}

/// 左侧功能区导航状态（tab 切换 + 刷新 + 工具栏动作）。
/// < > 会话历史导航已移交 ChatTabManager（本类不再管理历史栈）。
final class NavigationRouter: ObservableObject {
    @Published var currentTab: SidebarTab = .chat
    @Published var refreshCounts: [SidebarTab: Int] = [:]
    @Published private var refreshDisabledReasons: [SidebarTab: String] = [:]
    @Published private var toolbarActions: [SidebarTab: [ToolbarAction]] = [:]

    var loadedTabs: Set<SidebarTab> = [.chat]
    var currentTitle: String { currentTab.rawValue }

    var canRefreshCurrentTab: Bool {
        refreshDisabledReasons[currentTab] == nil
    }

    var refreshDisabledMessage: String? {
        refreshDisabledReasons[currentTab]
    }

    var currentToolbarActions: [ToolbarAction] {
        toolbarActions[currentTab] ?? []
    }

    func setRefreshDisabled(for tab: SidebarTab, message: String?) {
        if let msg = message {
            refreshDisabledReasons[tab] = msg
        } else {
            refreshDisabledReasons.removeValue(forKey: tab)
        }
    }

    func setToolbarActions(for tab: SidebarTab, actions: [ToolbarAction]) {
        toolbarActions[tab] = actions
    }

    func markLoaded(_ tab: SidebarTab) {
        loadedTabs.insert(tab)
    }

    func navigateTo(_ tab: SidebarTab) {
        guard tab != currentTab else { return }
        currentTab = tab
    }

    func refresh() {
        refreshCounts[currentTab, default: 0] += 1
    }
}
