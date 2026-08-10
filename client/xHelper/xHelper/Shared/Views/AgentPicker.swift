import SwiftUI

/// 输入栏智能体选择器：cpu 图标按钮 + Popover 滚动菜单。
///
/// 菜单首位展示当前会话激活智能体（勾选标记 + 高亮），下方按创建时间倒序分页，
/// 支持底部「加载更多」与点击切换。数据由外部（ChatView）维护，order 约定：
/// 传入的 agents 首位即为激活项。
struct AgentPickerButton: View {
    let context: InputBarContext
    /// 当前激活智能体 ID（决定勾选/高亮行）。
    var activeAgentID: String?
    /// 当前激活智能体名称（tooltip 显示）。
    var activeAgentName: String?
    /// 菜单条目（首位为激活智能体，其余按创建时间倒序）。
    var agents: [InputBarAgent]
    /// 是否还有更多可加载（控制底部「加载更多」）。
    var hasMore: Bool
    var isLoadingMore: Bool
    /// 点击切换智能体。
    var onSelect: (InputBarAgent) -> Void
    /// 加载更多。
    var onLoadMore: () -> Void

    @State private var isPresented: Bool

    /// - Parameter initiallyPresented: 初始即展开菜单（Preview 用，默认 false）。
    init(
        context: InputBarContext,
        activeAgentID: String? = nil,
        activeAgentName: String? = nil,
        agents: [InputBarAgent] = [],
        hasMore: Bool = false,
        isLoadingMore: Bool = false,
        onSelect: @escaping (InputBarAgent) -> Void = { _ in },
        onLoadMore: @escaping () -> Void = {},
        initiallyPresented: Bool = false
    ) {
        self.context = context
        self.activeAgentID = activeAgentID
        self.activeAgentName = activeAgentName
        self.agents = agents
        self.hasMore = hasMore
        self.isLoadingMore = isLoadingMore
        self.onSelect = onSelect
        self.onLoadMore = onLoadMore
        self._isPresented = State(initialValue: initiallyPresented)
    }

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: "cpu",
            tint: Color("appAccent"),
            tooltip: activeAgentName.map { "智能体：\($0)" } ?? "选择智能体",
            action: { isPresented = true }
        )
        .popover(isPresented: $isPresented, arrowEdge: .top) {
            menu
        }
    }

    private var menu: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 0) {
                if agents.isEmpty {
                    Text("暂无智能体")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 16)
                } else {
                    ForEach(agents) { agent in
                        AgentRow(
                            agent: agent,
                            isActive: agent.id == activeAgentID,
                            action: {
                                isPresented = false
                                onSelect(agent)
                            }
                        )
                        if agent.id != agents.last?.id {
                            Divider()
                        }
                    }
                }
                if isLoadingMore {
                    Divider()
                    HStack(spacing: 8) {
                        ProgressView()
                            .controlSize(.small)
                        Text("加载中…")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                        Spacer(minLength: 0)
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 10)
                } else if hasMore {
                    // 哨兵：滚到底进入可见区即触发加载下一页（LazyVStack 懒创建保证
                    // 列表够长未滚到时不会提前触发；ChatView 侧有 isLoadingPicker 去重）
                    Color.clear
                        .frame(width: 0, height: 0)
                        .onAppear { onLoadMore() }
                }
            }
        }
        .frame(width: 280)
        .frame(minHeight: 0, idealHeight: 320, maxHeight: 360)
        .padding(.vertical, 6)
    }
}

/// 菜单中的单个智能体行：激活项高亮 + 勾选标记，hover 反馈。
private struct AgentRow: View {
    let agent: InputBarAgent
    let isActive: Bool
    let action: () -> Void

    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Text(agent.name)
                    .font(.system(size: 13, weight: isActive ? .semibold : .regular))
                    .lineLimit(1)
                    .foregroundColor(.primary)
                Spacer(minLength: 0)
                if isActive {
                    Image(systemName: "checkmark")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundColor(Color("appAccent"))
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
            .background(rowBackground)
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
    }

    private var rowBackground: Color {
        if isActive { return Color("appAccent").opacity(0.12) }
        if hovering { return Color.primary.opacity(0.06) }
        return Color.clear
    }
}
