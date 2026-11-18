import SwiftUI
import WebKit
#if os(macOS)
import AppKit
#endif

/// 右侧栏：统一的胶囊 tab 条 + 当前 tab 内容（列表/详情/网页/工具页同一套身份）。
/// 首页是常驻的第一枚 tab（见 docs/design/right-sidebar-unified-tabs）。
struct RightSidebarView: View {
    @ObservedObject var manager: RightSidebarManager

    #if os(macOS)
    @ObservedObject private var faviconStore = FaviconStore.shared
    #endif

    var body: some View {
        Group {
            #if os(macOS)
            if manager.isSplit {
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

    /// 全屏态：tab 条 + 当前 tab 内容 + 底部 Agent 工作区条
    private var singleContentArea: some View {
        VStack(spacing: 0) {
            RightSidebarTabStrip(manager: manager)
            Divider()
            tabPane(active: manager.activeTabId)
            if !manager.agentTabs.isEmpty {
                agentWorkspaceStrip
            }
        }
    }

    @State private var agentWorkspaceExpanded = false

    /// Agent 工作区条：agent 打开的网页折叠展示，点击翻牌进主栏。
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

    private func agentOwnerHelp(_ tab: RightSidebarTab) -> String? {
        guard let owner = tab.agentOwner else { return nil }
        if owner.hasPrefix("main") { return "由主 Agent 打开" }
        if owner.hasPrefix("subagent:") {
            return "由子 Agent 打开（\(owner.dropFirst("subagent:".count))）"
        }
        return "由 Agent 打开（\(owner)）"
    }

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

    // MARK: - 内容

    #if os(macOS)
    @StateObject private var splitGhost = ResizeGhostState()
    @State private var splitSeamX: CGFloat = 0

    /// 分屏态：左右两窗格各带独立 tab 条 + 内容，中间可调宽分隔线。
    private var splitContentArea: some View {
        let minPane: Double = 200
        let leftW = max(minPane, min(manager.splitPaneWidth, manager.width - minPane))
        return HStack(spacing: 0) {
            splitPane(tabs: manager.leftGroupTabs, active: manager.leftDisplayTabId, side: .left)
                .frame(width: leftW)
            SeamMarker(x: $splitSeamX)
                .padding(.horizontal, 1)
            splitPane(tabs: manager.rightGroupTabs, active: manager.rightDisplayTabId, side: .right)
                .frame(minWidth: minPane)
        }
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

    private func splitPane(tabs: [RightSidebarTab], active: String?, side: SplitSide) -> some View {
        VStack(spacing: 0) {
            splitTabBar(tabs: tabs, side: side)
            Divider()
            // 分屏时每个窗格只挂本组 tab：保活/最近详情也算左侧归属，
            // 否则同一份内容（网页 NSView 是共享实例）会被两个窗格各挂一次（见 docs/bugs/030 同类坑）
            tabPane(active: active, allowed: Set(tabs.map(\.id)))
        }
    }

    /// 窗格内 tab 条：左窗格含「退出分屏」；右窗格含「退出分屏 / 新标签页」
    private func splitTabBar(tabs: [RightSidebarTab], side: SplitSide) -> some View {
        HStack(spacing: 0) {
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

            if side == .right {
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
            Button("重新加载") { BrowserToolProvider.shared.reloadTab(tab.id) }
            if side == .left {
                Button("移到右窗格") { manager.moveTabToRight(id: tab.id) }
            } else {
                Button("移到左窗格") { manager.moveTabToLeft(id: tab.id) }
                Button("退出分屏") { manager.clearSplit() }
            }
            Button("关闭") { BrowserToolProvider.shared.closeTab(tab.id) }
            Button("关闭窗格内其他标签页") {
                let group = side == .left ? manager.leftGroupTabs : manager.rightGroupTabs
                for t in group where t.id != tab.id { BrowserToolProvider.shared.closeTab(t.id) }
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

    /// 一个窗格：激活 tab + 保活 tab（媒体/最近详情）。
    /// `.id()` 必须：content 是同类型 AnyView，不加 id 切 tab 时 SwiftUI 会原地更新而不重挂。
    @ViewBuilder
    private func tabPane(active: String?, allowed: Set<String>? = nil) -> some View {
        ZStack {
            ForEach(manager.tabs.filter { tab in
                guard manager.renderableTabIds.contains(tab.id) else { return false }
                // 单栏：这一窗格挂全部可渲染项；分屏：只挂本组（active 永远例外，避免空窗格）
                guard let allowed else { return true }
                return tab.id == active || allowed.contains(tab.id)
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
