import SwiftUI
import Combine

/// 左侧功能区导航状态（tab 切换 + 刷新）。
/// < > 会话历史导航已移交 ChatTabManager（本类不再管理历史栈）。
final class NavigationRouter: ObservableObject {
    @Published var currentTab: SidebarTab = .chat
    @Published var refreshCounts: [SidebarTab: Int] = [:]
    @Published private var refreshDisabledReasons: [SidebarTab: String] = [:]

    var loadedTabs: Set<SidebarTab> = [.chat]
    var currentTitle: String { currentTab.rawValue }

    var canRefreshCurrentTab: Bool {
        refreshDisabledReasons[currentTab] == nil
    }

    var refreshDisabledMessage: String? {
        refreshDisabledReasons[currentTab]
    }

    func setRefreshDisabled(for tab: SidebarTab, message: String?) {
        if let msg = message {
            refreshDisabledReasons[tab] = msg
        } else {
            refreshDisabledReasons.removeValue(forKey: tab)
        }
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
