import SwiftUI

enum SidebarTab: String, CaseIterable, Identifiable {
    case chat = "新建聊天"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .chat:      return "square.and.pencil"
        }
    }
}

struct SidebarView: View {
    @Binding var selectedTab: SidebarTab
    @Binding var sidebarWidth: CGFloat
    @ObservedObject var chatTabManager: ChatTabManager
    @ObservedObject var conversationListVM: ConversationListViewModel

    @State private var hoveredConversationID: String?
    @State private var renameTarget: Conversation?
    @State private var renameText = ""
    /// 首次加载完成后再启用重排动画，避免初次填充时列表淡入。
    @State private var animateReorders = false

    private static let topPadding: CGFloat = 20

    private var isCollapsed: Bool {
        sidebarWidth <= Layout.sidebarSnapThreshold
    }

    var body: some View {
        VStack(spacing: 0) {
            if isCollapsed {
                collapsedContent
            } else {
                expandedContent
            }
        }
        .frame(width: sidebarWidth)
        .alert("重命名会话", isPresented: .init(
            get: { renameTarget != nil },
            set: { if !$0 { renameTarget = nil } }
        )) {
            TextField("标题", text: $renameText)
            Button("确定") {
                if let c = renameTarget {
                    Task { await conversationListVM.rename(id: c.id, title: renameText) }
                    if let tab = chatTabManager.tabs.first(where: { $0.conversationId == c.id }) {
                        tab.title = renameText
                    }
                }
                renameTarget = nil
            }
            Button("取消", role: .cancel) { renameTarget = nil }
        } message: {
            Text("输入新标题")
        }
    }

    // MARK: - Expanded

    private var expandedContent: some View {
        GeometryReader { geo in
            VStack(spacing: 0) {
                Spacer().frame(height: Self.topPadding)
                    .allowsHitTesting(false)

                // 功能导航（固定高度，不随会话列表滚动）
                VStack(spacing: 2) {
                    ForEach(SidebarTab.allCases) { tab in
                        expandedNavItem(tab, showText: showNavText)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 8)

                // 呼吸感：列表起点保持在卡片 ~30% 高度线（响应式）。
                // 112 = topPadding(20) + 导航区(92，随导航项数量变化需同步）
                Spacer()
                    .frame(height: max(40, geo.size.height * 0.30 - 112))
                    .allowsHitTesting(false)

                // 分区标题 + Divider
                Text("最近会话")
                    .font(.caption)
                    .foregroundColor(.secondary.opacity(0.7))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.top, 14)
                    .padding(.bottom, 6)

                Divider()
                .padding(.horizontal, 4)

                conversationListSection
            }
            .frame(width: geo.size.width, height: geo.size.height)
        }
    }

    private var conversationListSection: some View {
        ScrollViewReader { proxy in
            ScrollView(.vertical, showsIndicators: false) {
                LazyVStack(spacing: 2) {
                    ForEach(conversationListVM.conversations) { conv in
                        conversationRow(conv)
                            .id(conv.id)
                    }

                    if conversationListVM.hasMore {
                        ProgressView()
                            .controlSize(.small)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 10)
                            .onAppear {
                                Task { await conversationListVM.loadMore() }
                            }
                    }

                    if conversationListVM.isLoading && conversationListVM.conversations.isEmpty {
                        ProgressView()
                            .controlSize(.small)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 24)
                    }

                    if conversationListVM.conversations.isEmpty && !conversationListVM.isLoading {
                        Text("暂无会话")
                            .font(.caption)
                            .foregroundColor(.secondary)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 24)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 8)
                .animation(
                    animateReorders ? .easeInOut(duration: 0.28) : nil,
                    value: conversationListVM.conversations.map(\.id)
                )
            }
            .task {
                if !conversationListVM.hasLoadedOnce {
                    await conversationListVM.load()
                }
                animateReorders = true
            }
            // 快捷键/外部跳转切到某个会话时，把它滚动到可视区（侧栏点击本身已可见，滚动无副作用）
            .onChange(of: chatTabManager.currentConversationId) { _, newID in
                guard let newID, !newID.isEmpty else { return }
                withAnimation(.easeOut(duration: 0.2)) {
                    proxy.scrollTo(newID, anchor: .center)
                }
            }
        }
    }

    private func conversationRow(_ conv: Conversation) -> some View {
        let isCurrent = chatTabManager.currentConversationId == conv.id
        let isHovered = hoveredConversationID == conv.id
        let isPinned = conv.pinned == true
        let isEphemeral = conv.ephemeral == true
        return HStack(spacing: 2) {
            Button {
                selectedTab = .chat
                Task { await chatTabManager.openConversation(id: conv.id, title: conv.title, pushHistory: true, mode: conv.mode) }
            } label: {
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 4) {
                        if conv.ephemeral == true {
                            Image(systemName: "theatermasks.fill")
                                .font(.caption2)
                                .foregroundColor(.secondary)
                        }
                        Text(conv.title.isEmpty ? "新对话" : conv.title)
                            .font(.subheadline)
                            .lineLimit(1)
                            .foregroundColor(.primary)
                    }
                    Text(formatConversationDate(conv.updatedAt))
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(isCurrent ? Color("appAccent").opacity(0.18) : (isHovered ? Color.primary.opacity(0.06) : .clear))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }
            .buttonStyle(.plain)

            if isHovered {
                Button {
                    renameTarget = conv
                    renameText = conv.title
                } label: {
                    Image(systemName: "pencil")
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .frame(width: 20, height: 20)
                }
                .buttonStyle(.plain)
                .help("重命名")

                Button {
                    Task {
                        await conversationListVM.delete(id: conv.id)
                        chatTabManager.close(conversationId: conv.id)
                    }
                } label: {
                    Image(systemName: isEphemeral ? "trash" : "archivebox")
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .frame(width: 20, height: 20)
                }
                .buttonStyle(.plain)
                .help(isEphemeral ? "销毁" : "归档")
            }

            // 置顶按钮固定在最右：hover 时重命名/删除出现在其左侧，不再把它挤到左边。
            // 已置顶常显（强调色），未置顶仅 hover 时显示。
            if isHovered || isPinned {
                Button {
                    Task { await conversationListVM.setPinned(id: conv.id, pinned: !isPinned) }
                } label: {
                    Image(systemName: isPinned ? "pin.fill" : "pin")
                        .font(.caption)
                        .foregroundColor(isPinned ? Color("appAccent") : .secondary)
                        .frame(width: 20, height: 20)
                }
                .buttonStyle(.plain)
                .help(isPinned ? "取消置顶" : "置顶")
            }
        }
        .padding(.trailing, 6)
        .contentShape(Rectangle())
        .onHover { hovering in
            hoveredConversationID = hovering ? conv.id : nil
        }
    }

    // MARK: - Collapsed

    private var collapsedContent: some View {
        VStack(spacing: 0) {
            Spacer().frame(height: Self.topPadding)

            ScrollView {
                VStack(spacing: 8) {
                    ForEach(SidebarTab.allCases) { tab in
                        collapsedNavItem(tab)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 8)
            }

            Spacer()
        }
    }

    // MARK: - Nav Items

    /// 导航文字能否一行展示：侧栏宽度 ≥ 最宽导航项所需的宽度。
    /// 最宽文字宽按 CJK 每字≈字号估算（图标18 + 间距8 + 项padding20 + 段padding16）。
    private var showNavText: Bool {
        sidebarWidth >= navTextThreshold
    }

    private var navTextThreshold: CGFloat {
        let widestCount = SidebarTab.allCases.map { $0.rawValue.count }.max() ?? 0
        let textWidth = CGFloat(widestCount) * Layout.sidebarNavFontSize
        return 16 + 20 + 18 + 8 + textWidth
    }

    private func expandedNavItem(_ tab: SidebarTab, showText: Bool) -> some View {
        // 新建聊天是动作项，不显示选中底色/强调色，与其他功能 tab 区分开
        let isSelected = selectedTab == tab && tab != .chat
        return Button {
            if tab == .chat {
                selectedTab = .chat
                chatTabManager.newChat()
            } else {
                selectedTab = tab
            }
        } label: {
            HStack(spacing: 8) {
                Image(systemName: tab.icon)
                    .font(.body)
                    .frame(width: 18)
                    .offset(y: -1.5)
                    .foregroundColor(isSelected ? Color("appAccent") : .secondary)
                if showText {
                    Text(tab.rawValue)
                        .font(.system(size: Layout.sidebarNavFontSize, weight: .medium))
                        .foregroundColor(isSelected ? Color("appAccent") : .primary)
                }
                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(isSelected ? Color("appAccent").opacity(0.12) : .clear)
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
        .help(tab.rawValue)
        .contextMenu {
            if tab == .chat {
                Button("新建无痕会话") {
                    selectedTab = .chat
                    Task { await chatTabManager.newEphemeralChat() }
                }
            }
        }
    }

    private func collapsedNavItem(_ tab: SidebarTab) -> some View {
        let isSelected = selectedTab == tab && tab != .chat
        return Button {
            if tab == .chat {
                selectedTab = .chat
                chatTabManager.newChat()
            } else {
                selectedTab = tab
            }
        } label: {
            Image(systemName: tab.icon)
                .font(.title2)
                .frame(width: 30, height: 30)
                .frame(maxWidth: .infinity)
                .foregroundColor(isSelected ? Color("appAccent") : .secondary)
                .background(isSelected ? Color("appAccent").opacity(0.15) : .clear)
                .clipShape(RoundedRectangle(cornerRadius: 6))
                .help(tab.rawValue)
        }
        .buttonStyle(.plain)
        .contextMenu {
            if tab == .chat {
                Button("新建无痕会话") {
                    selectedTab = .chat
                    Task { await chatTabManager.newEphemeralChat() }
                }
            }
        }
    }
}
