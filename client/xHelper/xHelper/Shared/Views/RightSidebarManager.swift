import SwiftUI
import Combine

struct RightSidebarTab: Identifiable {
    let id: String
    var title: String
    let icon: String
    var content: AnyView
}

final class RightSidebarManager: ObservableObject {
    @Published var tabs: [RightSidebarTab] = []
    /// nil = 浏览面板态；浏览器 tab id = 浏览器态
    @Published var activeTabId: String?
    /// 浏览面板当前打开的浏览页（跨面板切换保留）
    @Published var browsePanelSelection: BrowsePanel?
    @Published var isOpen = false

    @Published var width: Double = 840 {
        didSet { UserDefaults.standard.set(width, forKey: "xhan_right_sidebar_width") }
    }

    init() {
        width = 840
    }

    var hasTabs: Bool { !tabs.isEmpty }

    /// 是否浏览面板态（无浏览器 tab 或未激活浏览器）
    var isBrowseActive: Bool {
        activeTabId == nil || tabs.isEmpty
    }

    func openTab(id: String, title: String, icon: String, content: AnyView) {
        if tabs.contains(where: { $0.id == id }) {
            activeTabId = id
        } else {
            tabs.append(RightSidebarTab(id: id, title: title, icon: icon, content: content))
            activeTabId = id
        }
        isOpen = true
    }

    func closeTab(_ id: String) {
        tabs.removeAll { $0.id == id }
        if activeTabId == id { activeTabId = tabs.last?.id }
        // 关掉所有浏览器 tab → 回到浏览面板（不收起侧栏）
        if tabs.isEmpty { activeTabId = nil }
    }

    /// 切回浏览面板（浏览器态 → 面板态）
    func showBrowsePanel() {
        activeTabId = nil
        BrowserToolProvider.shared.deselectTab()
    }

    func toggle() { isOpen.toggle() }

    func updateTabTitle(id: String, title: String) {
        guard let i = tabs.firstIndex(where: { $0.id == id }) else { return }
        var updated = tabs[i]
        updated.title = title
        tabs[i] = updated
    }

    func replaceTabContent(id: String, content: AnyView) {
        guard let i = tabs.firstIndex(where: { $0.id == id }) else { return }
        var updated = tabs[i]
        updated.content = content
        tabs[i] = updated
    }
}
