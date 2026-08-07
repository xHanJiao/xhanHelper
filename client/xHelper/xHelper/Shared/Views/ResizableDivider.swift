import SwiftUI
import Combine

#if os(macOS)
/// 可复用「可调宽分隔线」模块的共享坐标系名（SeamMarker 与 ResizableDivider 共用）。
enum ResizeDividerSpace {
    static let name = "resizable-divider-content"
}

/// 拖拽调宽的共享幽灵线状态。手柄上报实时宽度（按 id 存取，支持多个分隔线），
/// ResizableDivider 观察此状态绘制幽灵线——不触发整窗/聊天区重渲染。
final class ResizeGhostState: ObservableObject {
    @Published var widths: [String: Double] = [:]
}

/// 1pt 透明分隔线标记：维持面板间缝隙，并把自身 x 坐标（content 坐标系）上报，
/// 供 ResizableDivider 在容器 overlay 中定位浮层手柄。
struct SeamMarker: View {
    @Binding var x: CGFloat

    var body: some View {
        Rectangle()
            .fill(Color.clear)
            .frame(width: 1)
            .background(
                GeometryReader { proxy in
                    Color.clear
                        .onAppear { x = proxy.frame(in: .named(ResizeDividerSpace.name)).minX }
                        .onChange(of: proxy.frame(in: .named(ResizeDividerSpace.name)).minX) { _, new in x = new }
                }
            )
    }
}

/// 幽灵线：2pt 强调色竖线，底部 16pt 留白（线不到底）。
struct ResizeGhostLine: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Rectangle()
                .fill(Color("appAccent").opacity(0.7))
                .frame(width: 2)
            Spacer(minLength: 16) // 底部留白，线不到底
        }
        .allowsHitTesting(false)
    }
}

/// 可复用「可调宽分隔线」：浮层手柄 + 幽灵线，放在容器 overlay 里。
///
/// 用法：
///   1) 在 HStack 两个面板之间放 `SeamMarker(x: $seamX)`（维持 1pt 缝隙并上报 x）
///   2) 对容器 `.coordinateSpace(name: ResizeDividerSpace.name)`
///   3) 在容器 `.overlay(alignment: .topLeading)` 里放本组件
///
/// 消费方提供共享 `ResizeGhostState`（本组件观察它，只重渲染自身；
/// 消费方/聊天区不观察 → 拖拽不触发整窗重渲染）。
struct ResizableDivider: View {
    /// 幽灵线槽位 key（同一状态可驱动多个分隔线）。
    let id: String
    /// 提交的目标宽度绑定。
    @Binding var width: Double
    var minWidth: Double
    var maxWidth: Double
    /// 面板从右缘延伸（右侧栏）时为 true：拖左变宽，幽灵线方向相反。
    var reversed: Bool = false
    /// 共享幽灵线状态。
    @ObservedObject var ghost: ResizeGhostState
    /// 分隔线 x 坐标（由 SeamMarker 上报）。
    @Binding var seamX: CGFloat

    var body: some View {
        ZStack(alignment: .topLeading) {
            ResizeHandle(
                width: width,
                minWidth: minWidth,
                maxWidth: maxWidth,
                reversed: reversed,
                onLiveChange: { ghost.widths[id] = $0 },
                onCommit: { final in
                    ghost.widths[id] = nil
                    width = final
                }
            )
            .offset(x: seamX - 7.5)

            if let live = ghost.widths[id] {
                let delta = reversed ? (width - live) : (live - width)
                ResizeGhostLine()
                    .offset(x: seamX + delta)
            }
        }
    }
}

/// 不可拖拽分隔线：与 ResizableDivider 同款视觉（1pt 渐变竖线，常显淡色），
/// 但不响应拖拽、不绑定坐标。用于顶栏等「需要视觉分隔但不调宽」的场景。
struct UnresizableDivider: View {
    var height: CGFloat
    init(height: CGFloat = Layout.topBarCapsuleHeight) { self.height = height }

    var body: some View {
        Rectangle()
            .fill(Color.clear)
            .frame(width: 5, height: height)
            .overlay(alignment: .center) {
                Rectangle()
                    .fill(
                        LinearGradient(
                            colors: [
                                .clear,
                                Color.primary.opacity(0.25),
                                .clear
                            ],
                            startPoint: .top,
                            endPoint: .bottom
                        )
                    )
                    .frame(width: 1)
            }
    }
}
#endif
