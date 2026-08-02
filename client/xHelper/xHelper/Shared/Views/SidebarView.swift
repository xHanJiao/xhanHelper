import SwiftUI

enum SidebarTab: String, CaseIterable, Identifiable {
    case chat = "新建聊天"
    case agents = "智能体"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .chat:      return "square.and.pencil"
        case .agents:    return "cpu"
        }
    }
}

struct SidebarView: View {
    @Binding var selectedTab: SidebarTab
    @Binding var sidebarWidth: CGFloat
    let allowExpand: Bool
    @ObservedObject var chatTabManager: ChatTabManager
    @ObservedObject var conversationListVM: ConversationListViewModel
    /// 默认会话不可删除，由 ContentView 注入（deviceManager.defaultConversation?.id）
    let defaultConversationID: String?

    @GestureState private var dragOffset: CGFloat = 0
    @State private var lastExpandedWidth: CGFloat = Layout.sidebarDefaultWidth
    @State private var isExpandHovered = false
    @State private var hoveredConversationID: String?
    @State private var renameTarget: Conversation?
    @State private var renameText = ""

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
        .frame(width: max(Layout.sidebarMinWidth, sidebarWidth + dragOffset))
        .gesture(sidebarDragGesture)
        .animation(.easeInOut(duration: 0.15), value: sidebarWidth)
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
                        expandedNavItem(tab)
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
                    .padding(.horizontal, 8)

                conversationListSection
            }
            .frame(width: geo.size.width, height: geo.size.height)
        }
    }

    private var conversationListSection: some View {
        ScrollView {
            LazyVStack(spacing: 2) {
                ForEach(conversationListVM.conversations) { conv in
                    conversationRow(conv)
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
        }
        .task {
            if !conversationListVM.hasLoadedOnce {
                await conversationListVM.load()
            }
        }
    }

    private func conversationRow(_ conv: Conversation) -> some View {
        let isCurrent = chatTabManager.currentConversationId == conv.id
        let isHovered = hoveredConversationID == conv.id
        return HStack(spacing: 2) {
            Button {
                selectedTab = .chat
                Task { await chatTabManager.openConversation(id: conv.id, title: conv.title, pushHistory: true, mode: conv.mode) }
            } label: {
                VStack(alignment: .leading, spacing: 3) {
                    Text(conv.title.isEmpty ? "新对话" : conv.title)
                        .font(.subheadline)
                        .lineLimit(1)
                        .foregroundColor(isCurrent ? Color("appAccent") : .primary)
                    Text(formatConversationDate(conv.updatedAt))
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(isCurrent ? Color("appAccent").opacity(0.12) : (isHovered ? Color.primary.opacity(0.06) : .clear))
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

                if conv.id != defaultConversationID {
                    Button {
                        Task {
                            await conversationListVM.delete(id: conv.id)
                            chatTabManager.close(conversationId: conv.id)
                        }
                    } label: {
                        Image(systemName: "trash")
                            .font(.caption)
                            .foregroundColor(.secondary)
                            .frame(width: 20, height: 20)
                    }
                    .buttonStyle(.plain)
                    .help("删除")
                }
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

            // 底部展开按钮：仅收起态 + 窗口宽度允许展开时出现
            if allowExpand {
                Button {
                    sidebarWidth = max(lastExpandedWidth, 160)
                } label: {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 16, weight: .medium))
                        .foregroundColor(.secondary)
                        .frame(width: 30, height: 30)
                        .background(isExpandHovered ? AnyShapeStyle(Color.primary.opacity(0.1)) : AnyShapeStyle(.clear), in: Circle())
                }
                .buttonStyle(.plain)
                .onHover { isExpandHovered = $0 }
                .help("展开侧边栏")
                .padding(.bottom, 10)
            }
        }
    }

    // MARK: - Nav Items

    private func expandedNavItem(_ tab: SidebarTab) -> some View {
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
                Text(tab.rawValue)
                    .font(.title2)
                    .foregroundColor(isSelected ? Color("appAccent") : .primary)
                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(isSelected ? Color("appAccent").opacity(0.12) : .clear)
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
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
    }

    private var sidebarDragGesture: some Gesture {
        DragGesture(minimumDistance: 3)
            .updating($dragOffset) { value, state, _ in
                state = value.translation.width
            }
            .onEnded { value in
                let proposed = sidebarWidth + value.translation.width
                let clamped = min(max(proposed, Layout.sidebarMinWidth), Layout.sidebarMaxWidth)
                if clamped < Layout.sidebarSnapThreshold {
                    lastExpandedWidth = sidebarWidth
                    sidebarWidth = Layout.sidebarMinWidth
                } else if clamped < 160 {
                    guard allowExpand else { return }
                    sidebarWidth = 160
                    lastExpandedWidth = 160
                } else {
                    guard allowExpand else { return }
                    sidebarWidth = clamped
                    lastExpandedWidth = clamped
                }
            }
    }
}
