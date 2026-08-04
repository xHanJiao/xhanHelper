import SwiftUI
import Combine

struct RightSidebarTab: Identifiable {
    let id: String
    var title: String
    let icon: String
    var content: AnyView
    /// 当前页 host（favicon 查询键，随导航更新）
    var host: String?
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

    /// 聊天画中画（PiP）：进入时聊天区让位给右栏全宽，聊天缩成悬浮面板。
    @Published var isPipChat = false {
        didSet { pipLog("isPipChat: \(isPipChat)") }
    }

    /// 悬浮面板位置（容器坐标系，未钳制）。拖拽实时更新不落盘（每帧写 UserDefaults 太频繁），
    /// 由面板在拖拽结束/重钳制时调用 `persistPipPosition()`。
    @Published var pipPosition: CGPoint = .zero

    func persistPipPosition() {
        UserDefaults.standard.set(pipPosition.x, forKey: "xhan_pip_chat_x")
        UserDefaults.standard.set(pipPosition.y, forKey: "xhan_pip_chat_y")
    }

    /// 悬浮面板尺寸（用户拖拽缩放后的偏好尺寸），面板侧做容器钳制。
    @Published var pipSize: CGSize = .zero

    func persistPipSize() {
        UserDefaults.standard.set(pipSize.width, forKey: "xhan_pip_chat_w")
        UserDefaults.standard.set(pipSize.height, forKey: "xhan_pip_chat_h")
    }

    /// 悬浮面板收起态（记住上次状态）。
    @Published var pipCollapsed = false {
        didSet { UserDefaults.standard.set(pipCollapsed, forKey: "xhan_pip_chat_collapsed") }
    }

    init() {
        let stored = UserDefaults.standard.double(forKey: "xhan_right_sidebar_width")
        // PiP 时右栏可铺满整窗（可 > 1200），上限放宽防崩溃持久化值被丢弃
        width = stored >= 300 && stored <= 4000 ? stored : 840
        let x = UserDefaults.standard.double(forKey: "xhan_pip_chat_x")
        let y = UserDefaults.standard.double(forKey: "xhan_pip_chat_y")
        // x/y 任一非法（未保存或越界）时标记为未设置，由面板 onAppear 给默认值。
        if x > 0 && y > 0 {
            pipPosition = CGPoint(x: x, y: y)
        } else {
            pipPosition = CGPoint(x: -1, y: -1)
        }
        let w = UserDefaults.standard.double(forKey: "xhan_pip_chat_w")
        let h = UserDefaults.standard.double(forKey: "xhan_pip_chat_h")
        if w >= Layout.floatingPanelMinWidth && h >= Layout.floatingPanelMinHeight {
            pipSize = CGSize(width: w, height: h)
        } else {
            pipSize = CGSize(width: Layout.floatingPanelDefaultWidth, height: Layout.floatingPanelDefaultHeight)
        }
        pipCollapsed = UserDefaults.standard.bool(forKey: "xhan_pip_chat_collapsed")
    }

    var hasTabs: Bool { !tabs.isEmpty }

    /// 是否浏览面板态（无浏览器 tab 或未激活浏览器）
    var isBrowseActive: Bool {
        activeTabId == nil || tabs.isEmpty
    }

    func openTab(id: String, title: String, icon: String, content: AnyView, activate: Bool = true) {
        if tabs.contains(where: { $0.id == id }) {
            if activate { activeTabId = id }
        } else {
            let tab = RightSidebarTab(id: id, title: title, icon: icon, content: content)
            if Self.specialTabIDs.contains(id) {
                // 特殊 tab（下载/收藏/历史）固定最右：追加到末尾
                tabs.append(tab)
            } else if let firstSpecial = tabs.firstIndex(where: { Self.specialTabIDs.contains($0.id) }) {
                // 网页 tab 插入到特殊 tab 块之前
                tabs.insert(tab, at: firstSpecial)
            } else {
                tabs.append(tab)
            }
            if activate { activeTabId = id }
        }
        isOpen = true
    }

    /// 特殊 tab（浏览器内工具页），固定最右、不可拖拽重排
    static let specialTabIDs = [downloadTabID, bookmarkTabID, historyTabID, configTabID]

    /// 网页 tab 之间拖拽重排（特殊 tab 不参与）
    func moveTab(from id: String, to targetID: String) {
        guard id != targetID else { return }
        guard !Self.specialTabIDs.contains(id), !Self.specialTabIDs.contains(targetID) else { return }
        guard let fromIdx = tabs.firstIndex(where: { $0.id == id }),
              let toIdx = tabs.firstIndex(where: { $0.id == targetID }) else { return }
        let tab = tabs.remove(at: fromIdx)
        tabs.insert(tab, at: toIdx)
    }

    /// Cmd+Shift+[ / Cmd+Shift+]：切换上一个/下一个 tab（含特殊 tab，环绕）
    func switchTab(delta: Int) {
        guard !tabs.isEmpty else { return }
        let currentIndex = tabs.firstIndex { $0.id == activeTabId } ?? 0
        let nextIndex = (currentIndex + delta + tabs.count) % tabs.count
        let next = tabs[nextIndex]
        activeTabId = next.id
        if Self.specialTabIDs.contains(next.id) {
            BrowserToolProvider.shared.deselectTab()
        } else {
            BrowserToolProvider.shared.selectTab(next.id)
        }
    }

    func closeTab(_ id: String) {
        // 下载 tab：仅当它是唯一 tab 且还有活跃下载时禁关（保持浏览器不收起）；
        // 有其它网页 tab 时允许关闭（下载在 store 独立跟踪，关面板不影响）。
        if id == Self.downloadTabID,
           DownloadHistoryStore.shared.hasActiveDownloads,
           !tabs.contains(where: { $0.id != Self.downloadTabID }) {
            return
        }
        tabs.removeAll { $0.id == id }
        if activeTabId == id { activeTabId = tabs.last?.id }
        // 关掉所有浏览器 tab：有活跃下载时自动开下载 tab 保活浏览器，否则回到浏览面板（不收起侧栏）
        if tabs.isEmpty {
            if DownloadHistoryStore.shared.hasActiveDownloads {
                openDownloadTab()
            } else {
                activeTabId = nil
            }
        }
    }

    /// 打开/激活下载 tab（浏览器内，chrome://downloads 式）
    func openDownloadTab() {
        if let existing = tabs.first(where: { $0.id == Self.downloadTabID }) {
            activeTabId = existing.id
        } else {
            openTab(
                id: Self.downloadTabID,
                title: "下载",
                icon: "arrow.down.circle",
                content: Self.downloadTabContent()
            )
        }
    }

    /// 下载 tab 固定 id
    static let downloadTabID = "downloads"

    /// 下载 tab 内容（macOS 专属：DownloadHistoryPanelView）
    private static func downloadTabContent() -> AnyView {
        #if os(macOS)
        AnyView(DownloadHistoryPanelView())
        #else
        AnyView(EmptyView())
        #endif
    }

    /// 打开/激活收藏夹 tab
    func openBookmarkTab() {
        if let existing = tabs.first(where: { $0.id == Self.bookmarkTabID }) {
            activeTabId = existing.id
        } else {
            openTab(id: Self.bookmarkTabID, title: "收藏夹", icon: "bookmark",
                    content: Self.bookmarkTabContent())
        }
    }

    /// 打开/激活历史 tab
    func openHistoryTab() {
        if let existing = tabs.first(where: { $0.id == Self.historyTabID }) {
            activeTabId = existing.id
        } else {
            openTab(id: Self.historyTabID, title: "历史", icon: "clock",
                    content: Self.historyTabContent())
        }
    }

    /// 打开/激活浏览器配置 tab（内置默认值展示 + 用户自定义管理）
    func openConfigTab() {
        if let existing = tabs.first(where: { $0.id == Self.configTabID }) {
            activeTabId = existing.id
        } else {
            openTab(id: Self.configTabID, title: "浏览器配置", icon: "shield",
                    content: Self.configTabContent())
        }
    }

    static let bookmarkTabID = "bookmarks"
    static let historyTabID = "history"
    static let configTabID = "config-browser"

    private static func bookmarkTabContent() -> AnyView {
        #if os(macOS)
        AnyView(BookmarkPanelView())
        #else
        AnyView(EmptyView())
        #endif
    }

    private static func historyTabContent() -> AnyView {
        #if os(macOS)
        AnyView(HistoryPanelView())
        #else
        AnyView(EmptyView())
        #endif
    }

    private static func configTabContent() -> AnyView {
        #if os(macOS)
        AnyView(ConfigPanelView())
        #else
        AnyView(EmptyView())
        #endif
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

    func updateTabHost(id: String, host: String) {
        guard !host.isEmpty, let i = tabs.firstIndex(where: { $0.id == id }) else { return }
        var updated = tabs[i]
        updated.host = host
        tabs[i] = updated
    }

    func replaceTabContent(id: String, content: AnyView) {
        guard let i = tabs.firstIndex(where: { $0.id == id }) else { return }
        var updated = tabs[i]
        updated.content = content
        tabs[i] = updated
    }
}
