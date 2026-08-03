import SwiftUI

#if os(macOS)
/// 右侧栏 + 左缘调宽手柄的独立子视图。
/// 拖拽期间面板宽度不变，只画一条幽灵线指示目标边界（避免三张毛玻璃卡片
/// 逐帧重布局/重合成导致整窗闪动）；松手才把最终宽度提交到 manager.width。
struct RightResizablePanel: View {
    @ObservedObject var manager: RightSidebarManager
    /// 当前窗口/布局下允许的最大宽度（由 ContentView 传入）。
    let maxWidth: Double

    let billVM: BillViewModel
    let memoryVM: MemoryViewModel
    let noteVM: NoteViewModel
    let documentVM: DocumentViewModel
    let themeVM: ThemeViewModel
    let baseURL: String
    let token: String

    @State private var liveWidth: Double?

    var body: some View {
        ZStack(alignment: .topLeading) {
            HStack(spacing: 0) {
                ResizeHandle(
                    width: manager.width,
                    minWidth: 340,
                    maxWidth: maxWidth,
                    reversed: true,
                    onLiveChange: { liveWidth = $0 },
                    onCommit: { final in
                        liveWidth = nil
                        manager.width = final
                    }
                )
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
            }

            if let live = liveWidth {
                Rectangle()
                    .fill(Color("appAccent").opacity(0.7))
                    .frame(width: 2)
                    .offset(x: manager.width - live)
                    .allowsHitTesting(false)
            }
        }
    }
}
#endif
