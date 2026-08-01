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
    @Published var activeTabId: String?
    @Published var isOpen = false

    @Published var width: Double = 840 {
        didSet { UserDefaults.standard.set(width, forKey: "xhan_right_sidebar_width") }
    }

    init() {
        width = 840
    }

    var hasTabs: Bool { !tabs.isEmpty }

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
        if tabs.isEmpty { isOpen = false }
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
