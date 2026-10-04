import SwiftUI
import Combine
import WebKit

struct RightSidebarTab: Identifiable {
    let id: String
    var title: String
    let icon: String
    var content: AnyView
    /// 当前页 host（favicon 查询键，随导航更新）
    var host: String?
    /// agent 打开的标签（淡蓝底色区分；用户点击翻牌后置 false）
    var isAgentTab: Bool = false
    /// agent 来源 owner（"main" / "subagent:<convID>"）。翻牌/交还均保留，仅用于归属展示
    var agentOwner: String? = nil
    /// 手递手标签：agent 检测到验证码/登录墙升入主栏，需用户处理（⚠ 角标）
    var needsUserAttention: Bool = false
}

final class RightSidebarManager: ObservableObject {
    @Published var tabs: [RightSidebarTab] = []
    /// Agent 工作区标签（agent 打开的标签，独立于用户主栏；可折叠条展示）
    @Published var agentTabs: [RightSidebarTab] = []
    /// nil = 浏览面板态；浏览器 tab id = 浏览器态
    @Published var activeTabId: String?
    /// 分屏右窗格 tab 分区（有序 id）。空 = 全屏态；非空 = 分屏（左=池减右分区，右=本分区）。
    @Published var splitTabIds: [String] = []
    /// 分屏左窗格显示规则用的回退 tab（引擎 activeTabId 落入右分区时左窗格仍保持此 tab）。
    @Published var leftActiveTabId: String?
    /// 分屏右窗格显示的 tab。
    @Published var rightActiveTabId: String?
    /// 分屏左窗格宽度（ResizableDivider 绑定），进入分屏时默认 width/2，不持久化。
    @Published var splitPaneWidth: Double = 0
    /// 浏览面板当前打开的浏览页（跨面板切换保留）
    @Published var browsePanelSelection: BrowsePanel?
    @Published var isOpen = false
    /// 后台保活 tab（有媒体正在播放，切走后仍需留在视图层级继续播放）。
    /// 由 BrowserToolProvider 在切 tab 时注入 JS 检测后增删。
    @Published var keptAliveTabIds: Set<String> = []

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

    /// 是否分屏态（右窗格非空）
    var isSplit: Bool { !splitTabIds.isEmpty }

    /// 左窗格 tab 组（池减右分区，按池序，含特殊 tab）
    var leftGroupTabs: [RightSidebarTab] {
        tabs.filter { !splitTabIds.contains($0.id) }
    }

    /// 右窗格 tab 组（按 splitTabIds 序，仅网页 tab）
    var rightGroupTabs: [RightSidebarTab] {
        splitTabIds.compactMap { id in tabs.first { $0.id == id } }
    }

    /// 左窗格显示规则：引擎 active 在左组就跟随，否则保持左窗格最近 tab
    var leftDisplayTabId: String? {
        if let id = activeTabId, !splitTabIds.contains(id) { return id }
        return leftActiveTabId
    }

    /// 右窗格显示规则：引擎 active 在右组就跟随，否则保持右窗格最近 tab
    var rightDisplayTabId: String? {
        if let id = activeTabId, splitTabIds.contains(id) { return id }
        return rightActiveTabId
    }

    /// 移到右窗格：右分区空 → 进入分屏；已有 → 追加。守卫：网页 tab、左组移走前仍 ≥1。
    func moveTabToRight(id: String) {
        guard !Self.specialTabIDs.contains(id),
              splitTabIds.firstIndex(of: id) == nil,
              tabs.contains(where: { $0.id == id }),
              tabs.count - splitTabIds.count >= 2 else { return }
        if splitTabIds.isEmpty {
            splitPaneWidth = splitPaneWidth > 0 ? splitPaneWidth : width / 2
        }
        splitTabIds.append(id)
        rightActiveTabId = id
        // 移走的是左窗格显示/引擎 active 时，左窗格取下一个左组 tab
        if leftActiveTabId == id || activeTabId == id {
            let nextLeft = leftGroupTabs.first
            leftActiveTabId = nextLeft?.id
            if activeTabId == id, let nextLeft {
                activeTabId = nextLeft.id
                BrowserToolProvider.shared.selectTab(nextLeft.id)
            }
        }
    }

    /// 移到左窗格：从右分区移除并成为左窗格/引擎 active；右分区空 → 自动退出分屏。
    func moveTabToLeft(id: String) {
        guard let idx = splitTabIds.firstIndex(of: id) else { return }
        splitTabIds.remove(at: idx)
        if rightActiveTabId == id { rightActiveTabId = splitTabIds.last }
        leftActiveTabId = id
        activeTabId = id
        BrowserToolProvider.shared.selectTab(id)
    }

    /// 窗格内激活 tab：激活前把另一窗格当前显示记住（引擎 active 跳窗格时它不丢）。
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

    /// 右窗格内拖拽重排（右分区自己的顺序）
    func moveTabInRightPane(from id: String, to targetID: String) {
        guard id != targetID,
              let fromIdx = splitTabIds.firstIndex(of: id),
              let toIdx = splitTabIds.firstIndex(of: targetID) else { return }
        splitTabIds.remove(at: fromIdx)
        splitTabIds.insert(id, at: toIdx)
    }

    /// 退出分屏（右分区 tab 全部回到左组，回全屏单栏）
    func clearSplit() {
        splitTabIds = []
        rightActiveTabId = nil
    }

    func openTab(id: String, title: String, icon: String, content: AnyView, activate: Bool = true, isAgentTab: Bool = false, agentOwner: String? = nil, intoRightPane: Bool = false) {
        if isAgentTab {
            // agent 标签进工作区（不参与主栏/分屏/特殊 tab 逻辑），激活忽略（工作区恒不抢视图）
            if !agentTabs.contains(where: { $0.id == id }) {
                agentTabs.append(RightSidebarTab(id: id, title: title, icon: icon, content: content, isAgentTab: true, agentOwner: agentOwner))
            }
            // agent/subagent 后台开标签不自动展开右栏，只由用户或手递手展开
            return
        }
        if tabs.contains(where: { $0.id == id }) {
            if activate { activeTabId = id }
        } else {
            let tab = RightSidebarTab(id: id, title: title, icon: icon, content: content, isAgentTab: isAgentTab, agentOwner: agentOwner)
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
        if intoRightPane { moveTabToRight(id: id) }
        isOpen = true
    }

    // MARK: - Agent 工作区：翻牌进主栏 / 交还工作区 / 手递手角标

    /// agent 工作区标签 → 用户主栏（翻牌语义：isAgentTab 置 false）
    func promoteAgentTab(_ id: String) {
        guard let i = agentTabs.firstIndex(where: { $0.id == id }) else { return }
        var tab = agentTabs.remove(at: i)
        tab.isAgentTab = false
        if Self.specialTabIDs.contains(id) {
            tabs.append(tab)
        } else if let firstSpecial = tabs.firstIndex(where: { Self.specialTabIDs.contains($0.id) }) {
            tabs.insert(tab, at: firstSpecial)
        } else {
            tabs.append(tab)
        }
    }

    /// 主栏标签 → 交还 agent 工作区（手递手收尾）。UI 激活 tab 的切换由 provider 处理（switchActiveTab）。
    func demoteAgentTab(_ id: String) {
        guard let i = tabs.firstIndex(where: { $0.id == id }) else { return }
        var tab = tabs.remove(at: i)
        tab.isAgentTab = true
        tab.needsUserAttention = false
        agentTabs.append(tab)
        // 分屏维护：被交还 tab 若在右分区，修正右窗格 active
        if let idx = splitTabIds.firstIndex(of: id) { splitTabIds.remove(at: idx) }
        if rightActiveTabId == id { rightActiveTabId = splitTabIds.last }
        if splitTabIds.isEmpty { rightActiveTabId = nil }
    }

    /// 手递手 ⚠ 角标开关（升级进主栏时 true，resolve 后 false）
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

    /// 特殊 tab（浏览器内工具页），固定最右、不可拖拽重排
    static let specialTabIDs = [downloadTabID, bookmarkTabID, historyTabID, configTabID, mediaTabID]

    /// 网页 tab 之间拖拽重排（特殊 tab 不参与）
    func moveTab(from id: String, to targetID: String) {
        guard id != targetID else { return }
        guard !Self.specialTabIDs.contains(id), !Self.specialTabIDs.contains(targetID) else { return }
        guard let fromIdx = tabs.firstIndex(where: { $0.id == id }),
              let toIdx = tabs.firstIndex(where: { $0.id == targetID }) else { return }
        let tab = tabs.remove(at: fromIdx)
        tabs.insert(tab, at: toIdx)
    }

    /// Cmd+Shift+[ / Cmd+Shift+]：在 activeTabId 所属窗格内切换上一个/下一个 tab（环绕）
    func switchTab(delta: Int) {
        guard !tabs.isEmpty else { return }
        let inRightPane = isSplit && activeTabId.map { splitTabIds.contains($0) } ?? false
        let group = inRightPane ? rightGroupTabs : leftGroupTabs
        guard !group.isEmpty else { return }
        let currentActive = inRightPane ? activeTabId : leftDisplayTabId
        let currentIndex = group.firstIndex { $0.id == currentActive } ?? 0
        let next = group[(currentIndex + delta + group.count) % group.count]
        activeTabId = next.id
        if inRightPane {
            rightActiveTabId = next.id
        } else {
            leftActiveTabId = next.id
        }
        if Self.specialTabIDs.contains(next.id) {
            BrowserToolProvider.shared.deselectTab()
        } else {
            BrowserToolProvider.shared.selectTab(next.id)
        }
    }

    func closeTab(_ id: String) {
        // agent 工作区标签：直接从工作区移除（主栏逻辑不参与）
        if agentTabs.contains(where: { $0.id == id }) {
            agentTabs.removeAll { $0.id == id }
            return
        }
        // 下载 tab：仅当它是唯一 tab 且还有活跃下载时禁关（保持浏览器不收起）；
        // 有其它网页 tab 时允许关闭（下载在 store 独立跟踪，关面板不影响）。
        if id == Self.downloadTabID,
           DownloadHistoryStore.shared.hasActiveDownloads,
           !tabs.contains(where: { $0.id != Self.downloadTabID }) {
            return
        }
        tabs.removeAll { $0.id == id }
        // 分屏状态维护：被关 tab 属右分区 → 修右窗格 active，右分区空自动退分屏；
        // 属左分区/全屏 → 修左窗格与引擎 active（取下一个左组 tab）。
        if splitTabIds.contains(id) {
            if let idx = splitTabIds.firstIndex(of: id) { splitTabIds.remove(at: idx) }
            if rightActiveTabId == id { rightActiveTabId = splitTabIds.last }
            if activeTabId == id { activeTabId = rightActiveTabId ?? leftGroupTabs.last?.id }
            if splitTabIds.isEmpty { rightActiveTabId = nil }
        } else {
            if leftActiveTabId == id { leftActiveTabId = leftGroupTabs.last?.id }
            if activeTabId == id { activeTabId = leftGroupTabs.last?.id ?? tabs.last?.id }
        }
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

    /// 打开「本页资源」tab：每次从地址栏按钮进入都重新绑定来源页并触发重扫。
    /// sourceWebView 在此刻捕获——面板激活后 provider 的 activeSession 可能漂移，
    /// 扫描与下载都必须锚定打开面板那一刻的来源页。
    func openMediaPanel(sourceWebView: WKWebView) {
        let content = AnyView(MediaPickerPanelView(sourceWebView: sourceWebView))
        if tabs.contains(where: { $0.id == Self.mediaTabID }) {
            replaceTabContent(id: Self.mediaTabID, content: content)
            activeTabId = Self.mediaTabID
        } else {
            openTab(id: Self.mediaTabID, title: "本页资源", icon: "photo.on.rectangle.angled",
                    content: content)
        }
    }

    static let bookmarkTabID = "bookmarks"
    static let historyTabID = "history"
    static let configTabID = "config-browser"
    static let mediaTabID = "media"

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
        splitTabIds = []
        rightActiveTabId = nil
        leftActiveTabId = nil
        activeTabId = nil
        BrowserToolProvider.shared.deselectTab()
    }

    func toggle() { isOpen.toggle() }

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

    /// 更新 tab 的 agent 属性（用户点击翻牌后 agent tab → 用户 tab，底色随之切换）
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
