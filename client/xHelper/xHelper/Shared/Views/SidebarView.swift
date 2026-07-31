import SwiftUI

enum SidebarTab: String, CaseIterable, Identifiable {
    case chat = "聊天"
    case agents = "智能体"
    case bills = "账单"
    case memories = "记忆"
    case notes = "笔记"
    case documents = "文档"
    case themes = "主题"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .chat:      return "bubble.left.and.bubble.right"
        case .agents:    return "cpu"
        case .bills:     return "yensign.circle"
        case .memories:  return "brain.head.profile"
        case .notes:     return "note.text"
        case .documents: return "doc.text"
        case .themes:    return "tag"
        }
    }
}

struct SidebarView: View {
    @Binding var selectedTab: SidebarTab
    @Binding var sidebarWidth: CGFloat
    let allowExpand: Bool

    @Environment(\.openSettings) private var openSettings
    @GestureState private var dragOffset: CGFloat = 0
    @State private var lastExpandedWidth: CGFloat = Layout.sidebarDefaultWidth
    @State private var isToggleHovered = false

    private var isCollapsed: Bool {
        sidebarWidth <= Layout.sidebarSnapThreshold
    }

    private func toggleSidebar() {
        if isCollapsed {
            guard allowExpand else { return }
            sidebarWidth = max(lastExpandedWidth, 160)
        } else {
            lastExpandedWidth = sidebarWidth
            sidebarWidth = Layout.sidebarMinWidth
        }
    }

    var body: some View {
        ZStack(alignment: .trailing) {
            VStack(spacing: 0) {
                if isCollapsed {
                    collapsedContent
                } else {
                    expandedContent
                }
            }

            if allowExpand {
                toggleHandle
            }
        }
        .frame(width: max(Layout.sidebarMinWidth, sidebarWidth + dragOffset))
        .gesture(sidebarDragGesture)
        .animation(.easeInOut(duration: 0.15), value: sidebarWidth)
    }

    private var toggleHandle: some View {
        HStack(spacing: 0) {
            Spacer()
            Color.clear
                .frame(width: 52)
                .contentShape(Rectangle())
                .onHover { isToggleHovered = $0 }
                .onTapGesture { toggleSidebar() }
                .overlay(alignment: .trailing) {
                    Image(systemName: isCollapsed ? "chevron.right" : "chevron.left")
                        .font(.caption2.weight(.bold))
                        .foregroundColor(.secondary)
                        .frame(width: 26, height: 26)
                        .background(.thickMaterial, in: Circle())
                        .shadow(color: .black.opacity(0.12), radius: 4, y: 1)
                        .opacity(isToggleHovered ? 1 : 0)
                        .allowsHitTesting(false)
                }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var expandedContent: some View {
        VStack(spacing: 0) {
            Spacer().frame(height: 40)
                .allowsHitTesting(false)

            ScrollView {
                VStack(spacing: 2) {
                    ForEach(SidebarTab.allCases) { tab in
                        expandedNavItem(tab)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 8)
            }

            Spacer()

            expandedSettingsButton
        }
    }

    private var collapsedContent: some View {
        VStack(spacing: 0) {
            Spacer().frame(height: 40)

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

            collapsedSettingsButton
        }
    }

    private func expandedNavItem(_ tab: SidebarTab) -> some View {
        Button {
            selectedTab = tab
        } label: {
            HStack(spacing: 10) {
                Image(systemName: tab.icon)
                    .font(.body)
                    .frame(width: 20)
                    .foregroundColor(selectedTab == tab ? Color("appAccent") : .secondary)
                Text(tab.rawValue)
                    .font(.title3)
                    .foregroundColor(selectedTab == tab ? Color("appAccent") : .primary)
                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(selectedTab == tab ? Color("appAccent").opacity(0.12) : .clear)
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
    }

    private func collapsedNavItem(_ tab: SidebarTab) -> some View {
        Button {
            selectedTab = tab
        } label: {
            Image(systemName: tab.icon)
                .font(.title2)
                .frame(width: 30, height: 30)
                .frame(maxWidth: .infinity)
                .foregroundColor(selectedTab == tab ? Color("appAccent") : .secondary)
                .background(selectedTab == tab ? Color("appAccent").opacity(0.15) : .clear)
                .clipShape(RoundedRectangle(cornerRadius: 6))
                .help(tab.rawValue)
        }
        .buttonStyle(.plain)
    }

    private var expandedSettingsButton: some View {
        Button { openSettings() } label: {
            HStack(spacing: 10) {
                Image(systemName: "gear")
                    .font(.title3)
                    .frame(width: 20)
                    .foregroundColor(.secondary)
                Text("设置")
                    .font(.title3)
                    .foregroundColor(.secondary)
                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
        }
        .buttonStyle(.plain)
        .padding(.bottom, 8)
    }

    private var collapsedSettingsButton: some View {
        Button { openSettings() } label: {
            Image(systemName: "gear")
                .font(.title2)
                .frame(width: 30, height: 30)
                .frame(maxWidth: .infinity)
                .foregroundColor(.secondary)
                .help("设置")
        }
        .buttonStyle(.plain)
        .padding(.bottom, 8)
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
