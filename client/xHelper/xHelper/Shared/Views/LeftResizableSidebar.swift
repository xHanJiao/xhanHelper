import SwiftUI

#if os(macOS)
/// 左侧栏卡片视图。拖拽调宽手柄在 ContentView 覆盖层（不占布局空间），
/// 幽灵线通过共享的 ResizeGhostState 驱动。
struct LeftResizableSidebar: View {
    @Binding var sidebarWidth: CGFloat
    @Binding var selectedTab: SidebarTab
    let allowExpand: Bool
    @ObservedObject var chatTabManager: ChatTabManager
    @ObservedObject var conversationListVM: ConversationListViewModel
    let defaultConversationID: String?
    /// 拖拽调宽共享状态（观察幽灵线宽度）。
    @ObservedObject var ghost: ResizeGhostState

    @State private var sidebarHovered = false

    var body: some View {
        ZStack(alignment: .trailing) {
            SidebarView(
                selectedTab: $selectedTab,
                sidebarWidth: $sidebarWidth,
                allowExpand: allowExpand,
                chatTabManager: chatTabManager,
                conversationListVM: conversationListVM,
                defaultConversationID: defaultConversationID
            )
            .padding(.bottom, 12)
            .padding(.horizontal, 12)
            .modifier(CardBackgroundModifier(isLifted: sidebarHovered))
            .padding(.top, 4)
            .padding(.bottom, 12)
            .padding(.leading, 8)
            .padding(.trailing, 12)
            .onHover { sidebarHovered = $0 }

            if let live = ghost.leftWidth {
                VStack(alignment: .leading, spacing: 0) {
                    Rectangle()
                        .fill(Color("appAccent").opacity(0.7))
                        .frame(width: 2)
                    Spacer(minLength: 16) // 底部留白，线不到底
                }
                .offset(x: live - sidebarWidth)
                .allowsHitTesting(false)
            }
        }
        .zIndex(1) // 幽灵线向右溢出到聊天区时，保证渲染在聊天区之上
    }
}
#endif
