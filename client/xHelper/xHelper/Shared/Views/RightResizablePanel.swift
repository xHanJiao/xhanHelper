import SwiftUI

#if os(macOS)
/// 右侧栏卡片视图。拖拽调宽手柄在 ContentView 覆盖层（不占布局空间），
/// 幽灵线通过共享的 ResizeGhostState 驱动。
struct RightResizablePanel: View {
    @ObservedObject var manager: RightSidebarManager
    /// 拖拽调宽共享状态（观察幽灵线宽度）。
    @ObservedObject var ghost: ResizeGhostState

    let billVM: BillViewModel
    let memoryVM: MemoryViewModel
    let noteVM: NoteViewModel
    let documentVM: DocumentViewModel
    let themeVM: ThemeViewModel
    let baseURL: String
    let token: String

    var body: some View {
        ZStack(alignment: .topLeading) {
            RightSidebarView(
                manager: manager,
                billVM: billVM,
                memoryVM: memoryVM,
                noteVM: noteVM,
                documentVM: documentVM,
                themeVM: themeVM,
                baseURL: baseURL,
                token: token
            )
            .padding(.top, 4)
            .padding(.leading, 12)
            .padding(.trailing, 8)

            if let live = ghost.rightWidth {
                VStack(alignment: .leading, spacing: 0) {
                    Rectangle()
                        .fill(Color("appAccent").opacity(0.7))
                        .frame(width: 2)
                    Spacer(minLength: 16) // 底部留白，线不到底
                }
                .offset(x: manager.width - live)
                .allowsHitTesting(false)
            }
        }
    }
}
#endif
