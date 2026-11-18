import SwiftUI
import Combine
import WebKit

/// 右栏 tab 的种类。**「是不是网页」只认 `.web`**（`isWebTab`），
/// 其余判断（详情/列表/工具页）也从这里分，不再用 id 前缀或 `specialTabIDs` 拼。
enum RightSidebarTabKind: Equatable {
    /// 首页（常驻第一枚，不可关）
    case home
    /// 列表/工具面板（账单、书架、文档列表、灵阅列表…）
    case panel(BrowsePanel)
    /// 文档详情
    case document
    /// 书阅读
    case book
    /// 灵阅详情页
    case artifact
    /// 网页
    case web
    /// 浏览器工具页（下载 / 收藏 / 历史 / 配置 / 本页资源）
    case tool
}

struct RightSidebarTab: Identifiable {
    let id: String
    var kind: RightSidebarTabKind
    var title: String
    let icon: String
    var content: AnyView
    /// 当前页 host（favicon 查询键，随导航更新）
    var host: String?
    /// agent 打开的标签（淡蓝底色区分；用户点击翻牌后置 false）
    var isAgentTab: Bool = false
    /// agent 来源 owner（"main:<convID>" / "subagent:<convID>"）
    var agentOwner: String? = nil
    /// 手递手标签：agent 检测到验证码/登录墙升入主栏，需用户处理（⚠ 角标）
    var needsUserAttention: Bool = false
    /// 该标签用的浏览器内核（cef = Chromium / compat = WKWebView）。**创建后不变**（D15 tab 粘性）。
    var engine: BrowserEngine = .compat
    /// 关 tab 的收尾（书进度/文档选中/多选/搜索态）。网页 tab 走 provider 的 session teardown。
    var onClose: (() -> Void)? = nil
}

/// tab 内容工厂：VMs 只在 `ContentView` 那一层，manager 只拿 `AnyView`。
struct RightSidebarContentFactory {
    let home: () -> AnyView
    let panel: (BrowsePanel) -> AnyView
    let document: (Document) -> AnyView
    #if os(macOS)
    /// 书阅读只在 macOS（`BookMeta` / `BookReaderView` 都是 macOS-only）
    let book: (BookMeta) -> AnyView
    #endif
}

final class RightSidebarManager: ObservableObject {
    @Published var tabs: [RightSidebarTab] = []
    /// Agent 工作区标签（agent 打开的网页，独立于用户主栏）
    @Published var agentTabs: [RightSidebarTab] = []
    /// 当前激活 tab（恒指向 `tabs` 里的某一项；首页也算一个 tab）
    @Published var activeTabId: String?
    /// 分屏右窗格 tab 分区（有序 id）。空 = 全屏态；非空 = 分屏（左=池减右分区，右=本分区）。
    @Published var splitTabIds: [String] = []
    @Published var leftActiveTabId: String?
    @Published var rightActiveTabId: String?
    @Published var splitPaneWidth: Double = 0
    @Published var isOpen = true
    /// 后台保活 tab（有媒体正在播放，切走后仍需留在视图层级继续播放）
    @Published var keptAliveTabIds: Set<String> = []
    /// 最近看过的详情 tab（最多 3 个）：切走时仍挂视图层级，滚动位置/阅读态不丢（D6）
    @Published var recentTabIds: [String] = []

    @Published var width: Double = 840 {
        didSet { UserDefaults.standard.set(width, forKey: "xhan_right_sidebar_width") }
    }

    /// 聊天画中画（PiP）
    @Published var isPipChat = false {
        didSet { pipLog("isPipChat: \(isPipChat)") }
    }

    @Published var pipPosition: CGPoint = .zero

    func persistPipPosition() {
        UserDefaults.standard.set(pipPosition.x, forKey: "xhan_pip_chat_x")
        UserDefaults.standard.set(pipPosition.y, forKey: "xhan_pip_chat_y")
    }

    @Published var pipSize: CGSize = .zero

    func persistPipSize() {
        UserDefaults.standard.set(pipSize.width, forKey: "xhan_pip_chat_w")
        UserDefaults.standard.set(pipSize.height, forKey: "xhan_pip_chat_h")
    }

    @Published var pipCollapsed = false {
        didSet { UserDefaults.standard.set(pipCollapsed, forKey: "xhan_pip_chat_collapsed") }
    }

    /// 内容工厂（`ContentView.onAppear` 注入；走到打开路径还没它就是接线 bug）
    private var contentFactory: RightSidebarContentFactory?

    init() {
        let stored = UserDefaults.standard.double(forKey: "xhan_right_sidebar_width")
        width = stored >= 300 && stored <= 4000 ? stored : 840
        let x = UserDefaults.standard.double(forKey: "xhan_pip_chat_x")
        let y = UserDefaults.standard.double(forKey: "xhan_pip_chat_y")
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

    // MARK: - 基本查询

    static let homeTabID = "home"

    /// 有非首页 tab（⌘T 的触发条件沿用「浏览器里还有东西」）
    var hasTabs: Bool { tabs.contains { $0.kind != .home } }

    var activeTab: RightSidebarTab? {
        guard let id = activeTabId else { return nil }
        return tabs.first { $0.id == id }
    }

    /// 当前激活的列表/工具面板（首页与详情返回 nil）
    var activePanel: BrowsePanel? {
        if case .panel(let panel)? = activeTab?.kind { return panel }
        return nil
    }

    /// 首页态（没有 tab 或停在首页）
    var isBrowseActive: Bool {
        guard let tab = activeTab else { return true }
        return tab.kind == .home
    }

    func isWebTab(_ id: String?) -> Bool {
        guard let id else { return false }
        return tabs.first { $0.id == id }?.kind == .web
    }

    func tab(id: String) -> RightSidebarTab? {
        tabs.first { $0.id == id }
    }

    var isSplit: Bool { !splitTabIds.isEmpty }

    var leftGroupTabs: [RightSidebarTab] {
        tabs.filter { $0.kind != .home && !splitTabIds.contains($0.id) }
    }

    var rightGroupTabs: [RightSidebarTab] {
        splitTabIds.compactMap { id in tabs.first { $0.id == id } }
    }

    var leftDisplayTabId: String? {
        if let id = activeTabId, !splitTabIds.contains(id) { return id }
        return leftActiveTabId
    }

    var rightDisplayTabId: String? {
        if let id = activeTabId, splitTabIds.contains(id) { return id }
        return rightActiveTabId
    }

    /// 需要挂在视图层级里的 tab：激活项 + 分屏另一窗格 + 媒体保活 + 最近 3 个详情 tab。
    /// 其余摘掉视图（不参与合成），实例由各自的 VM/控制器保留。
    var renderableTabIds: [String] {
        var ids: [String] = []
        func add(_ id: String?) {
            guard let id, !ids.contains(id), tabs.contains(where: { $0.id == id }) else { return }
            ids.append(id)
        }
        add(activeTabId)
        if isSplit {
            add(leftDisplayTabId)
            add(rightDisplayTabId)
        }
        for id in keptAliveTabIds where isWebTab(id) { add(id) }
        for id in recentTabIds {
            guard let kind = tab(id: id)?.kind else { continue }
            if kind == .document || kind == .book || kind == .artifact { add(id) }
        }
        return ids
    }

    // MARK: - 内容工厂 / 首页 tab

    func configure(factory: RightSidebarContentFactory) {
        contentFactory = factory
        installHomeTab()
    }

    /// 首页 tab 常驻第一位、不可关；没有它就先装一个，并把 active 指过去。
    func installHomeTab() {
        if let home = tabs.first(where: { $0.kind == .home }) {
            if activeTabId == nil { activeTabId = home.id }
            return
        }
        guard let factory = contentFactory else { return }
        let home = RightSidebarTab(
            id: Self.homeTabID,
            kind: .home,
            title: "首页",
            icon: "house",
            content: factory.home()
        )
        tabs.insert(home, at: 0)
        if activeTabId == nil { activeTabId = home.id }
    }

    private var requiredFactory: RightSidebarContentFactory {
        guard let contentFactory else {
            fatalError("RightSidebarManager: contentFactory 未注入（应在 ContentView.onAppear 接线）")
        }
        return contentFactory
    }

    // MARK: - 打开

    static func panelTabID(_ panel: BrowsePanel) -> String { "panel:\(panel.rawValue)" }

    static func documentTabID(_ id: String) -> String { "document:\(id)" }

    static func bookTabID(_ id: String) -> String { "book:\(id)" }

    /// 打开/激活列表 tab（幂等）。首页卡片、⌘1–5、设置、深链都走这里。
    func openPanel(_ panel: BrowsePanel) {
        let id = Self.panelTabID(panel)
        if tabs.contains(where: { $0.id == id }) {
            activateTab(id: id)
        } else {
            let tab = RightSidebarTab(
                id: id,
                kind: .panel(panel),
                title: panel.rawValue,
                icon: panel.icon,
                content: requiredFactory.panel(panel)
            )
            tabs.append(tab)
            activateTab(id: id)
        }
        isOpen = true
    }

    /// 打开/激活文档详情 tab；已在同一篇上直接激活。
    func openDocumentTab(_ doc: Document) {
        let id = Self.documentTabID(doc.id)
        if tabs.contains(where: { $0.id == id }) {
            activateTab(id: id)
            return
        }
        let tab = RightSidebarTab(
            id: id,
            kind: .document,
            title: doc.title.isEmpty ? "文档" : doc.title,
            icon: "doc.text",
            content: requiredFactory.document(doc)
        )
        // 排在父列表 tab 右边（没有就追加）
        if let i = tabs.firstIndex(where: { $0.id == Self.panelTabID(.documents) }) {
            tabs.insert(tab, at: i + 1)
        } else {
            tabs.append(tab)
        }
        activateTab(id: id)
        isOpen = true
    }

    #if os(macOS)
    /// 打开/激活书阅读 tab。
    func openBookTab(_ book: BookMeta) {
        let id = Self.bookTabID(book.id)
        if tabs.contains(where: { $0.id == id }) {
            activateTab(id: id)
            return
        }
        let tab = RightSidebarTab(
            id: id,
            kind: .book,
            title: book.title,
            icon: "book.closed",
            content: requiredFactory.book(book)
        )
        if let i = tabs.firstIndex(where: { $0.id == Self.panelTabID(.books) }) {
            tabs.insert(tab, at: i + 1)
        } else {
            tabs.append(tab)
        }
        activateTab(id: id)
        isOpen = true
    }
    #endif

    /// 回到首页（等价「关掉一切导航、回到入口网格」）
    func goHome() {
        splitTabIds = []
        rightActiveTabId = nil
        leftActiveTabId = nil
        installHomeTab()
        activeTabId = Self.homeTabID
        BrowserToolProvider.shared.deselectTab()
    }

    /// 全部关闭：网页/灵阅/工具页走 provider（session teardown、下载强取消、媒体停播），
    /// 列表/文档/书走各自 `onClose`，最后回首页（分屏一并清掉）。
    /// 有意不做两件事：不保留 ⌘⇧T 恢复栈（重置语义，与右键「退出浏览器」一致）；
    /// 不碰 Agent 工作区标签（那是 agent 正在跑任务的页面，工作区自带「全部关闭」）。
    func closeAllTabs() {
        guard hasTabs else { return }
        // 有下载时 provider 会先弹确认条、异步回调：后续收尾必须放进 completion，
        // 否则用户取消后会出现「网页还在、别的 tab 已没了」的半关状态。
        BrowserToolProvider.shared.closeAllUserTabs { [weak self] in
            guard let self else { return }
            for tab in self.tabs {
                switch tab.kind {
                case .panel, .document, .book: self.closeTab(tab.id)
                case .home, .artifact, .web, .tool: break  // 后三类已由 provider 收掉
                }
            }
            self.goHome()
        }
    }

    func activateTab(id: String) {
        guard tabs.contains(where: { $0.id == id }) else { return }
        activeTabId = id
        trackRecent(id)
        if isWebTab(id) {
            BrowserToolProvider.shared.selectTab(id)
        } else {
            BrowserToolProvider.shared.deselectTab()
        }
    }

    /// 最近详情 tab 的记忆（D6：只保活详情类，列表页没必要）
    private func trackRecent(_ id: String) {
        guard let kind = tab(id: id)?.kind else { return }
        guard kind == .document || kind == .book || kind == .artifact else { return }
        recentTabIds.removeAll { $0 == id }
        recentTabIds.insert(id, at: 0)
        if recentTabIds.count > 3 { recentTabIds.removeLast() }
    }

    func openTab(id: String, title: String, icon: String, content: AnyView, activate: Bool = true, isAgentTab: Bool = false, agentOwner: String? = nil, intoRightPane: Bool = false, engine: BrowserEngine = .compat, kind: RightSidebarTabKind = .web, onClose: (() -> Void)? = nil) {
        if isAgentTab {
            if !agentTabs.contains(where: { $0.id == id }) {
                agentTabs.append(RightSidebarTab(id: id, kind: .web, title: title, icon: icon,
                                                 content: content, isAgentTab: true, agentOwner: agentOwner, engine: engine))
            }
            return
        }
        if tabs.contains(where: { $0.id == id }) {
            if activate { activateTab(id: id) }
        } else {
            let tab = RightSidebarTab(id: id, kind: kind, title: title, icon: icon, content: content,
                                      isAgentTab: isAgentTab, agentOwner: agentOwner, engine: engine,
                                      onClose: onClose)
            tabs.append(tab)
            if activate { activateTab(id: id) }
        }
        if intoRightPane { moveTabToRight(id: id) }
        isOpen = true
    }

    // MARK: - Agent 工作区

    func promoteAgentTab(_ id: String) {
        guard let i = agentTabs.firstIndex(where: { $0.id == id }) else { return }
        var tab = agentTabs.remove(at: i)
        tab.isAgentTab = false
        tabs.append(tab)
    }

    func demoteAgentTab(_ id: String) {
        guard let i = tabs.firstIndex(where: { $0.id == id }) else { return }
        var tab = tabs.remove(at: i)
        tab.isAgentTab = true
        tab.needsUserAttention = false
        agentTabs.append(tab)
        if let idx = splitTabIds.firstIndex(of: id) { splitTabIds.remove(at: idx) }
        if rightActiveTabId == id { rightActiveTabId = splitTabIds.last }
        if splitTabIds.isEmpty { rightActiveTabId = nil }
        if activeTabId == id { activeTabId = leftGroupTabs.last?.id ?? Self.homeTabID }
    }

    func setTabNeedsAttention(id: String, needs: Bool) {
        if let i = tabs.firstIndex(where: { $0.id == id }) {
            var t = tabs[i]
            t.needsUserAttention = needs
            tabs[i] = t
        }
        if let i = agentTabs.firstIndex(where: { $0.id == id }) {
            var t = agentTabs[i]
            t.needsUserAttention = needs
            agentTabs[i] = t
        }
    }

    // MARK: - 分屏

    func moveTabToRight(id: String) {
        guard isWebTab(id),
              splitTabIds.firstIndex(of: id) == nil,
              tabs.contains(where: { $0.id == id }),
              leftGroupTabs.count >= 2 else { return }
        if splitTabIds.isEmpty {
            splitPaneWidth = splitPaneWidth > 0 ? splitPaneWidth : width / 2
        }
        splitTabIds.append(id)
        rightActiveTabId = id
        if leftActiveTabId == id || activeTabId == id {
            let nextLeft = leftGroupTabs.first { $0.id != id }
            leftActiveTabId = nextLeft?.id
            if activeTabId == id, let nextLeft {
                activeTabId = nextLeft.id
                if isWebTab(nextLeft.id) { BrowserToolProvider.shared.selectTab(nextLeft.id) }
            }
        }
    }

    func moveTabToLeft(id: String) {
        guard let idx = splitTabIds.firstIndex(of: id) else { return }
        splitTabIds.remove(at: idx)
        if rightActiveTabId == id { rightActiveTabId = splitTabIds.last }
        leftActiveTabId = id
        activeTabId = id
        BrowserToolProvider.shared.selectTab(id)
    }

    func activateTab(id: String, inRightPane: Bool) {
        guard tabs.contains(where: { $0.id == id }) else { return }
        if inRightPane {
            leftActiveTabId = leftDisplayTabId
            rightActiveTabId = id
        } else {
            rightActiveTabId = rightDisplayTabId
            leftActiveTabId = id
        }
        activeTabId = id
    }

    func moveTabInRightPane(from id: String, to targetID: String) {
        guard id != targetID,
              let fromIdx = splitTabIds.firstIndex(of: id),
              let toIdx = splitTabIds.firstIndex(of: targetID) else { return }
        splitTabIds.remove(at: fromIdx)
        splitTabIds.insert(id, at: toIdx)
    }

    func clearSplit() {
        splitTabIds = []
        rightActiveTabId = nil
    }

    // MARK: - 重排 / 切换 / 关闭

    /// tab 条拖拽重排（首页不参与）
    func moveTab(from id: String, to targetID: String) {
        guard id != targetID else { return }
        guard id != Self.homeTabID, targetID != Self.homeTabID else { return }
        guard let fromIdx = tabs.firstIndex(where: { $0.id == id }),
              let toIdx = tabs.firstIndex(where: { $0.id == targetID }) else { return }
        let tab = tabs.remove(at: fromIdx)
        tabs.insert(tab, at: toIdx)
    }

    func switchTab(delta: Int) {
        let inRightPane = isSplit && activeTabId.map { splitTabIds.contains($0) } ?? false
        let group = inRightPane ? rightGroupTabs : leftGroupTabs
        guard !group.isEmpty else { return }
        let currentActive = inRightPane ? activeTabId : leftDisplayTabId
        let currentIndex = group.firstIndex { $0.id == currentActive } ?? (delta > 0 ? -1 : 0)
        let next = group[(currentIndex + delta + group.count) % group.count]
        activeTabId = next.id
        if inRightPane {
            rightActiveTabId = next.id
        } else {
            leftActiveTabId = next.id
        }
        trackRecent(next.id)
        if isWebTab(next.id) {
            BrowserToolProvider.shared.selectTab(next.id)
        } else {
            BrowserToolProvider.shared.deselectTab()
        }
    }

    /// 关 tab（UI 的 × / ⌘W / 右键菜单）。网页 tab 也走这里：
    /// provider 会先做 session teardown 再回调本方法（见 `BrowserToolProvider.closeTab`）。
    func closeTab(_ id: String) {
        if agentTabs.contains(where: { $0.id == id }) {
            agentTabs.removeAll { $0.id == id }
            return
        }
        guard let idx = tabs.firstIndex(where: { $0.id == id }) else { return }
        let tab = tabs[idx]
        guard tab.kind != .home else { return }
        // 下载 tab：唯一非首页 tab 且还有活跃下载时禁关（保住浏览器会话）
        if tab.kind == .tool, id == Self.downloadTabID,
           DownloadHistoryStore.shared.hasActiveDownloads,
           !tabs.contains(where: { $0.kind != .home && $0.id != Self.downloadTabID }) {
            return
        }
        tab.onClose?()
        tabs.remove(at: idx)
        recentTabIds.removeAll { $0 == id }
        if splitTabIds.contains(id) {
            if let i = splitTabIds.firstIndex(of: id) { splitTabIds.remove(at: i) }
            if rightActiveTabId == id { rightActiveTabId = splitTabIds.last }
            if splitTabIds.isEmpty { rightActiveTabId = nil }
        }
        if activeTabId == id {
            // 关网页时优先落到另一个网页（浏览体验连续）；关内容 tab 时落到左邻
            let next: RightSidebarTab? = tab.kind == .web
                ? (tabs.first(where: { $0.kind == .web }))
                : (tabs[safe: idx - 1] ?? tabs[safe: idx])
            activeTabId = (next ?? tabs.first)?.id
            if let next, isWebTab(next.id) {
                BrowserToolProvider.shared.selectTab(next.id)
            } else {
                BrowserToolProvider.shared.deselectTab()
            }
        }
        if !tabs.contains(where: { $0.kind == .home }) { installHomeTab() }
        if activeTabId == nil { activeTabId = Self.homeTabID }
    }

    // MARK: - 浏览器工具页

    static let downloadTabID = "downloads"
    static let bookmarkTabID = "bookmarks"
    static let historyTabID = "history"
    static let configTabID = "config-browser"
    static let mediaTabID = "media"

    /// 工具页 id（非网页、非内容面板）
    static let toolTabIDs: [String] = [downloadTabID, bookmarkTabID, historyTabID, configTabID, mediaTabID]

    /// HTML 产物 tab 的 id 前缀
    static let artifactTabPrefix = "artifact:"

    static func isArtifactTab(_ id: String?) -> Bool {
        guard let id else { return false }
        return id.hasPrefix(artifactTabPrefix)
    }

    func openDownloadTab() {
        if let existing = tabs.first(where: { $0.id == Self.downloadTabID }) {
            activateTab(id: existing.id)
        } else {
            openTab(id: Self.downloadTabID, title: "下载", icon: "arrow.down.circle",
                    content: Self.downloadTabContent(), kind: .tool)
        }
    }

    private static func downloadTabContent() -> AnyView {
        #if os(macOS)
        AnyView(DownloadHistoryPanelView())
        #else
        AnyView(EmptyView())
        #endif
    }

    func openBookmarkTab() {
        if let existing = tabs.first(where: { $0.id == Self.bookmarkTabID }) {
            activateTab(id: existing.id)
        } else {
            openTab(id: Self.bookmarkTabID, title: "收藏夹", icon: "bookmark",
                    content: Self.bookmarkTabContent(), kind: .tool)
        }
    }

    func openHistoryTab() {
        if let existing = tabs.first(where: { $0.id == Self.historyTabID }) {
            activateTab(id: existing.id)
        } else {
            openTab(id: Self.historyTabID, title: "历史", icon: "clock",
                    content: Self.historyTabContent(), kind: .tool)
        }
    }

    func openConfigTab() {
        if let existing = tabs.first(where: { $0.id == Self.configTabID }) {
            activateTab(id: existing.id)
        } else {
            openTab(id: Self.configTabID, title: "浏览器配置", icon: "shield",
                    content: Self.configTabContent(), kind: .tool)
        }
    }

    func openMediaPanel(sourceWebView: WKWebView) {
        let content = AnyView(MediaPickerPanelView(sourceWebView: sourceWebView))
        if tabs.contains(where: { $0.id == Self.mediaTabID }) {
            replaceTabContent(id: Self.mediaTabID, content: content)
            activateTab(id: Self.mediaTabID)
        } else {
            openTab(id: Self.mediaTabID, title: "本页资源", icon: "photo.on.rectangle.angled",
                    content: content, kind: .tool)
        }
    }

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

    func toggle() { isOpen.toggle() }

    // MARK: - 更新

    func updateTabTitle(id: String, title: String) {
        if let i = tabs.firstIndex(where: { $0.id == id }) {
            var updated = tabs[i]
            updated.title = title
            tabs[i] = updated
        }
        if let i = agentTabs.firstIndex(where: { $0.id == id }) {
            var updated = agentTabs[i]
            updated.title = title
            agentTabs[i] = updated
        }
    }

    func updateTabContent(id: String, content: AnyView) {
        if let i = tabs.firstIndex(where: { $0.id == id }) {
            var updated = tabs[i]
            updated.content = content
            tabs[i] = updated
        }
    }

    func updateTabHost(id: String, host: String) {
        guard !host.isEmpty else { return }
        if let i = tabs.firstIndex(where: { $0.id == id }) {
            var updated = tabs[i]
            updated.host = host
            tabs[i] = updated
        }
        if let i = agentTabs.firstIndex(where: { $0.id == id }) {
            var updated = agentTabs[i]
            updated.host = host
            agentTabs[i] = updated
        }
    }

    func updateTabAgentState(id: String, isAgentTab: Bool) {
        if let i = tabs.firstIndex(where: { $0.id == id }) {
            var updated = tabs[i]
            updated.isAgentTab = isAgentTab
            tabs[i] = updated
        }
        if let i = agentTabs.firstIndex(where: { $0.id == id }) {
            var updated = agentTabs[i]
            updated.isAgentTab = isAgentTab
            agentTabs[i] = updated
        }
    }

    func replaceTabContent(id: String, content: AnyView) {
        if let i = tabs.firstIndex(where: { $0.id == id }) {
            var updated = tabs[i]
            updated.content = content
            tabs[i] = updated
        }
        if let i = agentTabs.firstIndex(where: { $0.id == id }) {
            var updated = agentTabs[i]
            updated.content = content
            agentTabs[i] = updated
        }
    }
}

private extension Array {
    subscript(safe index: Int) -> Element? {
        indices.contains(index) ? self[index] : nil
    }
}
