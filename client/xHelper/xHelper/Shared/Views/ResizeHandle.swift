import SwiftUI
#if os(macOS)
import AppKit
#endif

#if os(macOS)
/// 面板边缘的拖拽调宽手柄。
/// 使用独立 DragGesture（而非挂在面板本体上），避免与面板内部的
/// ScrollView 抢手势——这是左侧栏原来整栏挂手势导致拖拽调宽失效的根因。
///
/// 拖拽期间不写任何持久化/发布状态，只通过 `onLiveChange` 上报实时宽度；
/// 松手才通过 `onCommit` 提交。这样拖拽每帧只重渲染持有 live 状态的最小视图，
/// 不会触发整个窗口（尤其聊天区）逐帧重布局导致闪动。
struct ResizeHandle: View {
    /// 已提交的当前宽度（只读）。
    var width: Double
    var minWidth: Double
    var maxWidth: Double
    /// 面板从右缘延伸（右侧栏）时为 true：拖左变宽、拖右变窄，与左缘手柄方向相反。
    var reversed: Bool = false
    /// 拖拽过程中每帧回调（传入实时钳制后的宽度）。
    var onLiveChange: (Double) -> Void = { _ in }
    /// 松手回调（传入最终宽度）。
    var onCommit: (Double) -> Void = { _ in }
    /// 拖拽开始回调（首次 onChanged 触发，用于在拖拽前切换布局状态）。
    var onDragStart: () -> Void = {}

    @State private var dragStartWidth: Double?
    @State private var hovering = false

    /// 是否有可拖拽的宽度范围（max > min）。无空间时禁用（不响应悬停/拖拽）。
    private var isResizable: Bool { maxWidth > minWidth }

    var body: some View {
        Rectangle()
            .fill(Color.clear)
            .frame(width: 15)
            .contentShape(Rectangle())
            .overlay(alignment: .center) {
                Rectangle()
                    .fill(
                        LinearGradient(
                            colors: [
                                .clear,
                                hovering ? Color.primary.opacity(0.35) : Color.primary.opacity(0),
                                .clear
                            ],
                            startPoint: .top,
                            endPoint: .bottom
                        )
                    )
                    .frame(width: 1)
            }
            .allowsHitTesting(isResizable)
            .onHover { isHover in
                hovering = isHover
                if isHover {
                    NSCursor.resizeLeftRight.push()
                } else {
                    NSCursor.pop()
                }
            }
            .onDisappear {
                if hovering {
                    NSCursor.pop()
                }
            }
            .gesture(
                DragGesture(minimumDistance: 0)
                    .onChanged { value in
                        if dragStartWidth == nil {
                            onDragStart()
                        }
                        let base = dragStartWidth ?? width
                        dragStartWidth = base
                        let delta = (reversed ? -1 : 1) * value.translation.width
                        let target = min(max(base + delta, minWidth), maxWidth)
                        onLiveChange(target)
                    }
                    .onEnded { value in
                        guard let base = dragStartWidth else { return }
                        dragStartWidth = nil
                        let delta = (reversed ? -1 : 1) * value.translation.width
                        let final = min(max(base + delta, minWidth), maxWidth)
                        onCommit(final)
                    }
            )
    }
}
#endif
