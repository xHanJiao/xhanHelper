import SwiftUI
import Combine

struct ToolbarAction: Identifiable {
    let id: String
    let icon: String
    let action: () -> Void
}

final class NavigationRouter: ObservableObject {
    @Published var currentTab: SidebarTab = .chat
    @Published var canGoBack = false
    @Published var canGoForward = false

    private var stack: [SidebarTab] = [.chat]
    private var stackIndex: Int = 0
    @Published var refreshCounts: [SidebarTab: Int] = [:]
    @Published private var refreshDisabledReasons: [SidebarTab: String] = [:]
    @Published private var toolbarActions: [SidebarTab: [ToolbarAction]] = [:]
    private let maxStackSize = 10

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

        if stackIndex < stack.count - 1 {
            stack.removeSubrange(stackIndex + 1..<stack.count)
        }

        stack.append(tab)
        while stack.count > maxStackSize {
            stack.removeFirst()
        }
        stackIndex = stack.count - 1

        currentTab = tab
        updateNavState()
    }

    func goBack() {
        guard stackIndex > 0 else { return }
        stackIndex -= 1
        currentTab = stack[stackIndex]
        updateNavState()
    }

    func goForward() {
        guard stackIndex < stack.count - 1 else { return }
        stackIndex += 1
        currentTab = stack[stackIndex]
        updateNavState()
    }

    func refresh() {
        refreshCounts[currentTab, default: 0] += 1
    }

    private func updateNavState() {
        canGoBack = stackIndex > 0
        canGoForward = stackIndex < stack.count - 1
    }
}
