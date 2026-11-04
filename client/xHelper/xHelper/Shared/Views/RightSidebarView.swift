import SwiftUI
import WebKit
#if os(macOS)
import AppKit
#endif

/// 右侧栏：浏览面板（默认态，无 tab 条）+ 浏览器标签页（tab 条 + 返回按钮）。
struct RightSidebarView: View {
    @ObservedObject var manager: RightSidebarManager
    @ObservedObject var billVM: BillViewModel
    @ObservedObject var memoryVM: MemoryViewModel
    @ObservedObject var artifactVM: ArtifactViewModel
    @ObservedObject var documentVM: DocumentViewModel
    @ObservedObject var eventVM: EventViewModel
    @ObservedObject var agentVM: AgentViewModel
    @ObservedObject var imageVM: ImageViewModel
    let baseURL: String
    let token: String

    @ObservedObject private var downloadStore = DownloadHistoryStore.shared

    #if os(macOS)
    @ObservedObject private var faviconStore = FaviconStore.shared
    #endif

    var body: some View {
        Group {
            #if os(macOS)
            if !manager.isBrowseActive && manager.isSplit {
                // 分屏态：双窗格（各带 tab 条 + 内容），无外层单栏
                splitContentArea
            } else {
                singleContentArea
            }
            #else
            singleContentArea
            #endif
        }
        // 不画边缘：右栏直接坐在底图上（与中栏「描边爬窗顶」对称，见 docs/design/mac-card-top-wrap/design.md）。
        .frame(minWidth: Layout.rightSidebarMinWidth, idealWidth: manager.width, maxWidth: .infinity)
    }

    // MARK: - 浏览器 tab 栏（仅浏览器态显示）

    /// 退出浏览器确认（关闭全部标签，释放网页内存；sheet 确认，非阻塞）
    private func confirmExitBrowser() {
        #if os(macOS)
        let alert = NSAlert()
        alert.messageText = "退出浏览器"
        alert.informativeText = "将关闭所有打开的标签页并退出浏览器，重新进入时将是一个全新的空浏览状态。"
        alert.addButton(withTitle: "退出")
        alert.addButton(withTitle: "取消")
        let exit: () -> Void = { BrowserToolProvider.shared.exitBrowser() }
        guard let window = NSApp.keyWindow ?? NSApp.windows.first else {
            DispatchQueue.main.async {
                guard alert.runModal() == .alertFirstButtonReturn else { return }
                exit()
            }
            return
        }
        alert.beginSheetModal(for: window) { response in
            guard response == .alertFirstButtonReturn else { return }
            exit()
        }
        #else
        BrowserToolProvider.shared.exitBrowser()
        #endif
    }

    private var browserTabBar: some View {
        HStack(spacing: 0) {
            backButton
            exitBrowserButton

            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 0) {
                    ForEach(manager.tabs) { tab in
                        tabButton(tab)
                    }
                }
                .frame(height: 32)
            }
            // 原固定按钮（PiP/清数据/+/下载/⭐/书签/🕐）已移到顶栏中槽位（TopBarRegistry）
        }
        .padding(.leading, 6)
    }

    /// 返回浏览面板入口
    private var backButton: some View {
        Button {
            manager.showBrowsePanel()
        } label: {
            HStack(spacing: 3) {
                Image(systemName: "chevron.left").font(.system(size: 10, weight: .semibold))
                Text("返回").font(.system(size: 11))
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .foregroundColor(.secondary)
        .help("返回浏览面板")
    }

    /// 退出浏览器：关闭所有标签、彻底退出（区别于「返回」保留标签）。确认后执行。
    private var exitBrowserButton: some View {
        Button {
            confirmExitBrowser()
        } label: {
            HStack(spacing: 3) {
                Image(systemName: "rectangle.portrait.and.arrow.right")
                    .font(.system(size: 10, weight: .semibold))
                Text("退出").font(.system(size: 11))
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .foregroundColor(.secondary)
        .help("退出浏览器，关闭所有标签")
    }

    /// tab 按钮：网页 tab 可拖拽重排；特殊 tab（下载/收藏/历史）固定不可拖
    @ViewBuilder
    private func tabButton(_ tab: RightSidebarTab) -> some View {
        if RightSidebarManager.specialTabIDs.contains(tab.id) {
            browserTabButton(tab)
        } else {
            browserTabButton(tab)
                .draggable(tab.id)
                .dropDestination(for: String.self) { items, _ in
                    guard let dragged = items.first else { return false }
                    manager.moveTab(from: dragged, to: tab.id)
                    return true
                }
        }
    }

    private func browserTabButton(_ tab: RightSidebarTab) -> some View {
        let isActive = manager.activeTabId == tab.id
        // 下载 tab 禁关态：它是唯一 tab 且仍有下载（浏览器保活 A）
        let isDownloadTab = tab.id == RightSidebarManager.downloadTabID
        let hasOtherTabs = manager.tabs.contains { $0.id != RightSidebarManager.downloadTabID }
        let downloadLocked = isDownloadTab && downloadStore.hasActiveDownloads && !hasOtherTabs
        return HStack(spacing: 4) {
            faviconView(tab)
            if tab.needsUserAttention {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 9))
                    .foregroundColor(.orange)
            }
            Text(tab.title)
                .font(.system(size: 11))
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: 160)
            Button { BrowserToolProvider.shared.closeTab(tab.id) } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 8, weight: .bold))
                    .opacity(downloadLocked ? 0.35 : 1)
            }
            .buttonStyle(.plain)
            .help(downloadLocked ? "下载进行中，完成后可关闭" : "关闭")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background {
            ZStack {
                if isActive { Rectangle().fill(.regularMaterial) }
                if tab.agentOwner != nil { Color.blue.opacity(0.18) }
            }
            .clipShape(UnevenRoundedRectangle(
                topLeadingRadius: 6, bottomLeadingRadius: 0,
                bottomTrailingRadius: 0, topTrailingRadius: 6))
        }
        .help(agentOwnerHelp(tab) ?? "")
        .contentShape(Rectangle())
        .onTapGesture {
            manager.activeTabId = tab.id
            BrowserToolProvider.shared.selectTab(tab.id)
        }
        .contextMenu {
            if !RightSidebarManager.specialTabIDs.contains(tab.id) {
                Button("重新加载") { BrowserToolProvider.shared.reloadTab(tab.id) }
                Button("移到右窗格") { manager.moveTabToRight(id: tab.id) }
            }
            Button("关闭") { BrowserToolProvider.shared.closeTab(tab.id) }
                .disabled(downloadLocked)
            Button("关闭其他标签页") {
                for id in manager.tabs.map(\.id) where id != tab.id {
                    BrowserToolProvider.shared.closeTab(id)
                }
            }
            Button("关闭右侧标签页") {
                let ids = manager.tabs.map(\.id)
                guard let i = ids.firstIndex(of: tab.id) else { return }
                for id in ids[(i + 1)...] {
                    BrowserToolProvider.shared.closeTab(id)
                }
            }
        }
    }

    /// agent 来源 tab 的悬停提示：展示 owner（main:<convID> / subagent:<convID>）；用户 tab 返回 nil
    private func agentOwnerHelp(_ tab: RightSidebarTab) -> String? {
        guard let owner = tab.agentOwner else { return nil }
        if owner.hasPrefix("main") { return "由主 Agent 打开" }
        if owner.hasPrefix("subagent:") {
            return "由子 Agent 打开（\(owner.dropFirst("subagent:".count))）"
        }
        return "由 Agent 打开（\(owner)）"
    }

    /// tab 图标：优先显示网站 favicon，否则回退系统符号
    @ViewBuilder
    private func faviconView(_ tab: RightSidebarTab) -> some View {
        #if os(macOS)
        if let host = tab.host, let img = faviconStore.image(forHost: host) {
            Image(nsImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 12, height: 12)
        } else {
            Image(systemName: tab.icon).font(.system(size: 11))
        }
        #else
        Image(systemName: tab.icon).font(.system(size: 11))
        #endif
    }

    // MARK: - Content

    #if os(macOS)
    @StateObject private var splitGhost = ResizeGhostState()
    @State private var splitSeamX: CGFloat = 0
    #endif

    /// 全屏态（含浏览面板）：单 tab 条 + 内容 + 底部 Agent 工作区条
    private var singleContentArea: some View {
        VStack(spacing: 0) {
            if !manager.isBrowseActive {
                browserTabBar
                Divider()
            }
            tabPane(active: manager.isBrowseActive ? nil : manager.activeTabId)
            if !manager.isBrowseActive, !manager.agentTabs.isEmpty {
                agentWorkspaceStrip
            }
        }
    }

    @State private var agentWorkspaceExpanded = false

    /// Agent 工作区条（浏览器态底部）：agent 打开的标签折叠展示，点击翻牌进主栏。
    /// 空则整条隐藏；展开态显示 host favicon + 标题 + 关闭。
    private var agentWorkspaceStrip: some View {
        VStack(spacing: 0) {
            Divider()
            HStack(spacing: 6) {
                Button {
                    withAnimation(.easeInOut(duration: 0.15)) { agentWorkspaceExpanded.toggle() }
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: agentWorkspaceExpanded ? "chevron.down" : "chevron.right")
                            .font(.system(size: 8))
                        Image(systemName: "sparkles").font(.system(size: 9))
                        Text("Agent 工作区").font(.system(size: 10, weight: .medium))
                        Text("\(manager.agentTabs.count)")
                            .font(.system(size: 9))
                            .padding(.horizontal, 4).padding(.vertical, 1)
                            .background(Color.orange.opacity(0.2), in: Capsule())
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                Spacer()
                Button { BrowserToolProvider.shared.closeAllAgentTabs() } label: {
                    Image(systemName: "xmark.circle").font(.system(size: 11))
                }
                .buttonStyle(.plain)
                .help("关闭全部 Agent 标签")
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            if agentWorkspaceExpanded {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 4) {
                        ForEach(manager.agentTabs) { tab in
                            agentWorkspaceTabButton(tab)
                        }
                    }
                    .padding(.horizontal, 8)
                    .padding(.bottom, 5)
                }
            }
        }
        .background(.ultraThinMaterial)
    }

    /// 工作区单个 agent 标签：点击翻牌进主栏；X 关闭（回收）
    private func agentWorkspaceTabButton(_ tab: RightSidebarTab) -> some View {
        HStack(spacing: 4) {
            faviconView(tab)
            Text(tab.title)
                .font(.system(size: 10))
                .lineLimit(1)
                .frame(maxWidth: 120)
            Button { BrowserToolProvider.shared.closeTab(tab.id) } label: {
                Image(systemName: "xmark").font(.system(size: 8, weight: .bold))
            }
            .buttonStyle(.plain)
            .help("关闭并回收")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(Color.blue.opacity(0.18), in: Capsule())
        .contentShape(Capsule())
        .onTapGesture {
            BrowserToolProvider.shared.promoteAgentTab(tab.id, activate: true, byUser: true)
        }
        .help((agentOwnerHelp(tab) ?? "Agent 标签") + " · 点击翻牌进主栏查看")
    }

    #if os(macOS)
    /// 分屏态完整内容：左右两窗格各带独立 tab 条 + 内容，中间可调宽分隔线。
    /// ponytail: 不限制窗格最小宽（过窄时右键「退出分屏」即可）。
    private var splitContentArea: some View {
        let minPane: Double = 200
        // 左窗格实际宽 = 钳制后的 splitPaneWidth（拖拽手柄写它，布局必须显式挂宽才生效）
        let leftW = max(minPane, min(manager.splitPaneWidth, manager.width - minPane))
        return HStack(spacing: 0) {
            splitPane(tabs: manager.leftGroupTabs, active: manager.leftDisplayTabId, side: .left)
                .frame(width: leftW)
            SeamMarker(x: $splitSeamX)
                .padding(.horizontal, 1)   // 中缝总宽 3pt（1+1+1），视觉略宽、更易拖拽
            splitPane(tabs: manager.rightGroupTabs, active: manager.rightDisplayTabId, side: .right)
                .frame(minWidth: minPane)
        }
        // 关键：本 HStack 建立同名局部坐标系，shadow 掉外层 ContentView 的坐标系，
        // 使 SeamMarker 上报的 seamX 是「分屏 HStack 内部坐标」（≈左窗格宽），
        // ResizableDivider 的 offset(seamX-7.5) 才能把手柄/幽灵线放在真实中缝上。
        // 否则 seamX 是右栏在整窗的绝对 x，手柄被错位整整一个右栏左缘，拖拽失效。
        .coordinateSpace(name: ResizeDividerSpace.name)
        .overlay(alignment: .topLeading) {
            ResizableDivider(
                id: "browser-split",
                width: Binding(
                    get: { manager.splitPaneWidth },
                    set: { manager.splitPaneWidth = $0 }
                ),
                minWidth: minPane,
                maxWidth: max(minPane, manager.width - minPane),
                ghost: splitGhost,
                seamX: $splitSeamX
            )
        }
    }

    private enum SplitSide {
        case left, right
    }

    /// 单个分屏窗格：独立 tab 条 + 内容
    private func splitPane(tabs: [RightSidebarTab], active: String?, side: SplitSide) -> some View {
        VStack(spacing: 0) {
            splitTabBar(tabs: tabs, side: side)
            Divider()
            tabPane(active: active)
        }
    }

    /// 窗格内 tab 条：左窗格含 返回/退出；右窗格含 退出分屏/+ 新标签页
    private func splitTabBar(tabs: [RightSidebarTab], side: SplitSide) -> some View {
        HStack(spacing: 0) {
            if side == .left {
                backButton
                exitBrowserButton
            } else {
                Button {
                    manager.clearSplit()
                } label: {
                    HStack(spacing: 3) {
                        Image(systemName: "rectangle.split.2x1").font(.system(size: 10, weight: .semibold))
                        Text("退出分屏").font(.system(size: 11))
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 6)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundColor(.secondary)
                .help("退出分屏，回到全屏单栏")
                Button {
                    BrowserToolProvider.shared.openBlankTab(intoRight: true)
                } label: {
                    Image(systemName: "plus")
                        .font(.system(size: 11, weight: .semibold))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 6)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundColor(.secondary)
                .help("右窗格新标签页")
            }
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 0) {
                    ForEach(tabs) { tab in
                        splitTabButton(tab, side: side)
                    }
                }
                .frame(height: 32)
            }
        }
        .padding(.leading, 6)
    }

    /// 分屏窗格内 tab 按钮：点击激活该窗格；右键移到另一窗格；窗格内拖拽重排
    private func splitTabButton(_ tab: RightSidebarTab, side: SplitSide) -> some View {
        let isActive = (side == .left ? manager.leftDisplayTabId : manager.rightDisplayTabId) == tab.id
        return HStack(spacing: 4) {
            faviconView(tab)
            Text(tab.title)
                .font(.system(size: 11))
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: 120)
            Button { BrowserToolProvider.shared.closeTab(tab.id) } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 8, weight: .bold))
            }
            .buttonStyle(.plain)
            .help("关闭")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background {
            ZStack {
                if isActive { Rectangle().fill(.regularMaterial) }
                if tab.agentOwner != nil { Color.blue.opacity(0.18) }
            }
            .clipShape(UnevenRoundedRectangle(
                topLeadingRadius: 6, bottomLeadingRadius: 0,
                bottomTrailingRadius: 0, topTrailingRadius: 6))
        }
        .help(agentOwnerHelp(tab) ?? "")
        .contentShape(Rectangle())
        .onTapGesture {
            manager.activateTab(id: tab.id, inRightPane: side == .right)
            BrowserToolProvider.shared.selectTab(tab.id)
        }
        .contextMenu {
            if !RightSidebarManager.specialTabIDs.contains(tab.id) {
                Button("重新加载") { BrowserToolProvider.shared.reloadTab(tab.id) }
                if side == .left {
                    Button("移到右窗格") { manager.moveTabToRight(id: tab.id) }
                } else {
                    Button("移到左窗格") { manager.moveTabToLeft(id: tab.id) }
                    Button("退出分屏") { manager.clearSplit() }
                }
            }
            Button("关闭") { BrowserToolProvider.shared.closeTab(tab.id) }
            if !RightSidebarManager.specialTabIDs.contains(tab.id) {
                Button("关闭窗格内其他标签页") {
                    let group = side == .left ? manager.leftGroupTabs : manager.rightGroupTabs
                    // 与「关闭」同路径：走 provider 才会 teardown session / 记恢复栈，
                    // 直接 manager.closeTab 会把网页 session 漏在 provider 里
                    for t in group where t.id != tab.id { BrowserToolProvider.shared.closeTab(t.id) }
                }
            }
        }
        .draggable(tab.id)
        .dropDestination(for: String.self) { items, _ in
            guard let dragged = items.first else { return false }
            if side == .right {
                manager.moveTabInRightPane(from: dragged, to: tab.id)
            } else {
                manager.moveTab(from: dragged, to: tab.id)
            }
            return true
        }
    }
    #endif

    /// 单个窗格：浏览面板（仅全屏态）+ 该窗格激活 tab + 后台媒体保活 tab。
    /// 保活过滤排除左右两窗格显示 tab：跨窗格已挂载它，防同一 NSView 挂两个 superview 崩溃。
    @ViewBuilder
    private func tabPane(active: String?) -> some View {
        ZStack {
            if manager.isBrowseActive {
                BrowsePanelView(
                    manager: manager,
                    billVM: billVM,
                    memoryVM: memoryVM,
                    artifactVM: artifactVM,
                    documentVM: documentVM,
                    eventVM: eventVM,
                    agentVM: agentVM,
                    imageVM: imageVM,
                    baseURL: baseURL,
                    token: token
                )
            }
            // 节能：只渲染窗格激活 tab + 有媒体在播的后台保活 tab（keptAliveTabIds）。
            // 激活 tab 正常显示；保活 tab 以 opacity(0) 留在视图层级（WKWebView 不被摘除，
            // 媒体继续播放，切回浏览面板也续播），但不响应事件；其余 tab 摘除视图层级
            // （不参与合成渲染），实例保留在 BrowserToolProvider.tabs，切回时同 NSView
            // 重新挂载、页面状态不丢。
            // .id() 必须：content 是同类型 AnyView(BrowserTabView)，不加 id 切 tab 时
            // SwiftUI 会判定"同位置同类型→原地更新"，旧 webView 不卸、新 webView 不挂。
            ForEach(manager.tabs.filter { tab in
                tab.id == active || (manager.keptAliveTabIds.contains(tab.id)
                    && tab.id != manager.leftDisplayTabId && tab.id != manager.rightDisplayTabId)
            }) { tab in
                let isActive = tab.id == active
                tab.content
                    .opacity(isActive ? 1 : 0)
                    .allowsHitTesting(isActive)
                    .disabled(!isActive)
                    .zIndex(isActive ? 1 : 0)
                    .id(tab.id)
            }
        }
    }
}
