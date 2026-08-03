import SwiftUI

#if os(macOS)
/// 左侧栏 + 右缘调宽手柄的独立子视图。
/// 拖拽期间侧栏宽度不变，只画一条幽灵线指示目标边界（避免整窗闪动）；
/// 松手才把最终宽度提交到 sidebarWidth。
struct LeftResizableSidebar: View {
    @Binding var sidebarWidth: CGFloat
    @Binding var selectedTab: SidebarTab
    let allowExpand: Bool
    @ObservedObject var chatTabManager: ChatTabManager
    @ObservedObject var conversationListVM: ConversationListViewModel
    let defaultConversationID: String?

    @State private var liveWidth: CGFloat?
    @State private var sidebarHovered = false

    var body: some View {
        HStack(spacing: 0) {
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

                if let live = liveWidth {
                    Rectangle()
                        .fill(Color("appAccent").opacity(0.7))
                        .frame(width: 2)
                        .offset(x: live - sidebarWidth)
                        .allowsHitTesting(false)
                }
            }
            .zIndex(1) // 幽灵线向右溢出到聊天区时，保证渲染在聊天区之上

            if allowExpand {
                ResizeHandle(
                    width: Double(sidebarWidth),
                    minWidth: Double(Layout.sidebarMinWidth),
                    maxWidth: Double(Layout.sidebarMaxWidth),
                    onLiveChange: { liveWidth = CGFloat($0) },
                    onCommit: { final in
                        liveWidth = nil
                        sidebarWidth = CGFloat(final)
                    }
                )
            }
        }
    }
}
#endif
