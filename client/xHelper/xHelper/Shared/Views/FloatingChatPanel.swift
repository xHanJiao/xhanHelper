import SwiftUI

#if os(macOS)
/// 聊天画中画面板：复用 ChatPage（同 viewModel，streaming/L3 确认/计划便签自动生效），
/// 面板内紧凑字号跟随全局 Cmd +/-。支持拖动 + 边缘吸附 + 收起/展开 + 右下角缩放。
///
/// 反闪动设计（沿用 ResizableDivider 的幽灵线思路）：
/// 1. 拖动/缩放改走「幽灵外框」——拖拽中面板本体静止，只移动一条轻量描边外框跟随光标，
///    松手才把面板动画落到外框位置。面板材质/WKWebView 背板在拖拽全程不重渲染。
/// 2. 聊天内容抽成独立子视图（PipChatContent），与面板拖动状态解耦。
///    注意：不用 .equatable()——它与 @ObservedObject + TextEditor 焦点冲突，会破坏输入框聚焦。
/// 3. 实时位置/尺寸只写本地 @State，仅在交互端点写回共享 manager 并落盘。
struct FloatingChatPanel: View {
    @ObservedObject var viewModel: ChatViewModel
    @ObservedObject var manager: RightSidebarManager
    let loadError: String?
    let baseURL: String
    let token: String
    let containerSize: CGSize
    let onExit: () -> Void

    @StateObject private var compactFont = FontSizeManager.compact()
    @State private var isInitialized = false
    /// 收起圆点入场弹跳（进 PiP 时提醒用户面板落在哪）。
    @State private var popIn = false
    /// 入场涟漪圈（定位提示，动画结束即停）。
    @State private var ping = false
    /// 实时渲染位置/尺寸（仅交互端点变化，拖动帧不变）。
    @State private var pos: CGPoint = .zero
    @State private var size: CGSize = CGSize(width: Layout.floatingPanelDefaultWidth,
                                             height: Layout.floatingPanelDefaultHeight)
    /// 幽灵外框状态（拖动/缩放中非 nil）。
    @State private var dragStartPos: CGPoint?
    @State private var dragGhostOffset: CGSize?
    @State private var resizeStartSize: CGSize?
    @State private var resizeGhostSize: CGSize?
    /// 流式时圆点呼吸闪烁的目标透明度（onChange 驱动）。
    @State private var streamingOpacity: Double = 1

    // MARK: - 尺寸 / 位置钳制

    /// 用户偏好尺寸 clamp 到 [min, min(容器−2×边距, 硬上限)]；收起时固定圆点方形。
    private func clampSize(_ preferred: CGSize, container: CGSize, collapsed: Bool) -> CGSize {
        let margin = Layout.floatingPanelEdgeMargin
        let maxW = max(Layout.floatingPanelMinWidth,
                       min(Layout.floatingPanelMaxWidth, container.width - 2 * margin))
        let maxH = max(Layout.floatingPanelMinHeight,
                       min(Layout.floatingPanelMaxHeight, container.height - 2 * margin))
        let w = min(max(preferred.width, Layout.floatingPanelMinWidth), maxW)
        if collapsed {
            return CGSize(width: Layout.floatingPanelCollapsedDotSize,
                          height: Layout.floatingPanelCollapsedDotSize)
        }
        let h = min(max(preferred.height, Layout.floatingPanelMinHeight), maxH)
        return CGSize(width: w, height: h)
    }

    private func clampPosition(_ p: CGPoint, container: CGSize, panel: CGSize) -> CGPoint {
        let margin = Layout.floatingPanelEdgeMargin
        let minX = margin
        let minY = margin
        let maxX = max(minX, container.width - panel.width - margin)
        let maxY = max(minY, container.height - panel.height - margin)
        return CGPoint(x: min(max(p.x, minX), maxX), y: min(max(p.y, minY), maxY))
    }

    private func snappedPosition(_ p: CGPoint, container: CGSize, panel: CGSize) -> CGPoint {
        let margin = Layout.floatingPanelEdgeMargin
        let threshold = Layout.floatingPanelSnapThreshold
        let maxX = container.width - panel.width - margin
        let maxY = container.height - panel.height - margin
        var x = p.x
        var y = p.y
        if abs(p.x - margin) <= threshold { x = margin }
        else if abs(p.x - maxX) <= threshold { x = maxX }
        if abs(p.y - margin) <= threshold { y = margin }
        else if abs(p.y - maxY) <= threshold { y = maxY }
        return CGPoint(x: x, y: y)
    }

    // MARK: - 生命周期

    private func onAppearSetup() {
        guard !isInitialized else { return }
        isInitialized = true
        size = clampSize(manager.pipSize, container: containerSize, collapsed: manager.pipCollapsed)
        var p = manager.pipPosition
        if p.x < 0 || p.y < 0 {
            // 无存储值 → 默认右上角
            p = CGPoint(x: containerSize.width - size.width - Layout.floatingPanelEdgeMargin,
                        y: Layout.floatingPanelEdgeMargin)
        }
        pos = clampPosition(p, container: containerSize, panel: size)
        manager.pipPosition = pos
        manager.persistPipPosition()
        manager.pipSize = clampSize(manager.pipSize, container: containerSize, collapsed: false)
        manager.persistPipSize()
        withAnimation(.spring(response: 0.5, dampingFraction: 0.42)) { popIn = true }
        withAnimation(.easeOut(duration: 0.8).repeatCount(2, autoreverses: false)) { ping = true }
        pipLog("panel appear: container=\(Int(containerSize.width))x\(Int(containerSize.height)) pos=(\(Int(pos.x)),\(Int(pos.y))) size=\(Int(size.width))x\(Int(size.height)) collapsed=\(manager.pipCollapsed)")
    }

    /// 容器尺寸变化 / 收起展开时，把偏好尺寸与位置重新 clamp（偏好尺寸留在 manager，不在此持久化）。
    private func reClamp(container: CGSize) {
        let newSize = clampSize(manager.pipSize, container: container, collapsed: manager.pipCollapsed)
        let newPos = clampPosition(pos, container: container, panel: newSize)
        if newSize != size || newPos != pos {
            pipLog("re-clamp: size \(Int(size.width))x\(Int(size.height)) -> \(Int(newSize.width))x\(Int(newSize.height)), pos (\(Int(pos.x)),\(Int(pos.y))) -> (\(Int(newPos.x)),\(Int(newPos.y))), container=\(Int(container.width))x\(Int(container.height))")
            size = newSize
            pos = newPos
        }
    }

    // MARK: - 提交（写回共享 manager，仅在交互端点调用）

    private func commitPosition() {
        manager.pipPosition = pos
        manager.persistPipPosition()
    }

    private func commitSize() {
        manager.pipSize = size
        manager.persistPipSize()
    }

    // MARK: - 手势：幽灵拖动 + 幽灵缩放

    private var dragGesture: some Gesture {
        DragGesture()
            .onChanged { value in
                if dragStartPos == nil {
                    dragStartPos = pos
                    dragGhostOffset = .zero
                }
                let start = dragStartPos ?? pos
                let candidate = clampPosition(CGPoint(x: start.x + value.translation.width,
                                                      y: start.y + value.translation.height),
                                              container: containerSize, panel: size)
                dragGhostOffset = CGSize(width: candidate.x - start.x, height: candidate.y - start.y)
            }
            .onEnded { value in
                let start = dragStartPos ?? pos
                let candidate = clampPosition(CGPoint(x: start.x + value.translation.width,
                                                      y: start.y + value.translation.height),
                                              container: containerSize, panel: size)
                let snapped = snappedPosition(candidate, container: containerSize, panel: size)
                pipLog("drag end: ghost=(\(Int(snapped.x)),\(Int(snapped.y)))")
                dragStartPos = nil
                dragGhostOffset = nil
                if snapped != pos {
                    withAnimation(.easeOut(duration: 0.15)) { pos = snapped }
                }
                commitPosition()
            }
    }

    private var resizeGesture: some Gesture {
        DragGesture()
            .onChanged { value in
                if resizeStartSize == nil {
                    resizeStartSize = size
                    resizeGhostSize = size
                }
                let start = resizeStartSize ?? size
                resizeGhostSize = clampSize(CGSize(width: start.width + value.translation.width,
                                                   height: start.height + value.translation.height),
                                            container: containerSize, collapsed: false)
            }
            .onEnded { _ in
                let target = resizeGhostSize ?? size
                pipLog("resize end: size=(\(Int(target.width))x\(Int(target.height)))")
                resizeStartSize = nil
                resizeGhostSize = nil
                if target != size {
                    withAnimation(.easeOut(duration: 0.15)) {
                        size = target
                        pos = clampPosition(pos, container: containerSize, panel: target)
                    }
                }
                commitSize()
                commitPosition()
            }
    }

    private func toggleCollapse() {
        manager.pipCollapsed.toggle()
        pipLog("collapse toggle: collapsed=\(manager.pipCollapsed) streaming=\(viewModel.isStreaming)")
        reClamp(container: containerSize)
    }

    // MARK: - Body

    var body: some View {
        ZStack(alignment: .topLeading) {
            // 面板本体（拖拽帧静止）
            Group {
                if manager.pipCollapsed {
                    collapsedDot
                        .shadow(color: .black.opacity(0.18), radius: 6, y: 2)
                } else {
                    expandedBody
                        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
                        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                        .overlay(
                            RoundedRectangle(cornerRadius: 14, style: .continuous)
                                .stroke(.white.opacity(0.15), lineWidth: 0.5)
                        )
                        .shadow(color: .black.opacity(0.18), radius: 18, y: 8)
                }
            }
            .frame(width: size.width, height: size.height)
            // 缩放把手钉在面板自身 frame 的右下角（不随内容 VStack 溢出偏移）。
            // 此前把手挂在 expandedBody 上，内容溢出（计划便签/两行输入框/L3 确认条）
            // 会把把手顶出可见区，被下方 clipShape 裁掉 → 缩到太小后调不回来。
            .overlay(alignment: .bottomTrailing) {
                if !manager.pipCollapsed {
                    resizeHandle
                }
            }
            // 用 .position 定位而非 .offset：macOS 上 .offset 包裹 TextEditor 会破坏
            // first responder（点击输入框不出光标、无法输入）。
            .position(x: pos.x + size.width / 2, y: pos.y + size.height / 2)

            // 幽灵外框：拖动/缩放中跟随光标，松手才落位
            if let go = dragGhostOffset {
                ghostRect(width: size.width, height: size.height)
                    .position(x: pos.x + go.width + size.width / 2,
                              y: pos.y + go.height + size.height / 2)
            } else if let rs = resizeGhostSize {
                ghostRect(width: rs.width, height: rs.height)
                    .position(x: pos.x + rs.width / 2,
                              y: pos.y + rs.height / 2)
            }
        }
        .frame(width: containerSize.width, height: containerSize.height)
        .onAppear { onAppearSetup() }
        .onChange(of: containerSize) { _, new in reClamp(container: new) }
        .onChange(of: manager.pipCollapsed) { _, _ in reClamp(container: containerSize) }
    }

    private func ghostRect(width: CGFloat, height: CGFloat) -> some View {
        RoundedRectangle(cornerRadius: 14, style: .continuous)
            .stroke(Color("appAccent").opacity(0.7), style: StrokeStyle(lineWidth: 2, dash: [6, 4]))
            .frame(width: width, height: height)
            .allowsHitTesting(false)
    }

    // MARK: - 展开态

    private var expandedBody: some View {
        VStack(spacing: 0) {
            header
            Divider()
            PipChatContent(
                viewModel: viewModel,
                loadError: loadError,
                baseURL: baseURL,
                token: token,
                compactFont: compactFont
            )
        }
    }

    private var header: some View {
        HStack(spacing: 6) {
            Image(systemName: "bubble.left.and.bubble.right")
                .font(.system(size: 11))
                .foregroundColor(Color("appAccent"))
            Text(viewModel.conversationTitle ?? "对话")
                .font(.system(size: 12, weight: .semibold))
                .lineLimit(1)
            Spacer()
            Button { toggleCollapse() } label: {
                Image(systemName: "minus")
                    .font(.system(size: 10, weight: .semibold))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("收起")
            Button {
                pipLog("panel exit tapped")
                onExit()
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 9, weight: .bold))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("退出画中画")
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .contentShape(Rectangle())
        .gesture(dragGesture)
        .simultaneousGesture(TapGesture(count: 2).onEnded { toggleCollapse() })
        .help("双击收起/展开")
    }

    /// 右下角缩放把手。
    private var resizeHandle: some View {
        Image(systemName: "arrow.up.left.and.arrow.down.right")
            .font(.system(size: 9, weight: .semibold))
            .foregroundColor(.secondary.opacity(0.7))
            .frame(width: 18, height: 18)
            .contentShape(Rectangle())
            .gesture(resizeGesture)
            .help("拖拽右下角调整大小")
    }

    // MARK: - 收起态（小圆点，V2 可替换为 SpritePetView）

    private var collapsedDot: some View {
        ZStack {
            if ping {
                Circle()
                    .stroke(Color("appAccent").opacity(0.6), lineWidth: 2)
                    .frame(width: Layout.floatingPanelCollapsedDotSize, height: Layout.floatingPanelCollapsedDotSize)
                    .scaleEffect(ping ? 2.2 : 0.9)
                    .opacity(ping ? 0 : 0.6)
                    .allowsHitTesting(false)
            }
            Circle()
                .fill(.regularMaterial)
                .overlay(Circle().stroke(.white.opacity(0.15), lineWidth: 0.5))
            if viewModel.isStreaming {
                Circle()
                    .fill(Color("appAccent").opacity(0.3))
            }
            Image(systemName: "bubble.left.and.bubble.right")
                .font(.system(size: 16, weight: .medium))
                .foregroundColor(Color("appAccent"))
        }
        .opacity(viewModel.isStreaming ? streamingOpacity : 1)
        .onChange(of: viewModel.isStreaming) { _, streaming in
            if streaming {
                streamingOpacity = 0.55
                withAnimation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true)) {
                    streamingOpacity = 1
                }
            } else {
                streamingOpacity = 1
            }
        }
        .frame(width: Layout.floatingPanelCollapsedDotSize, height: Layout.floatingPanelCollapsedDotSize)
        .scaleEffect(popIn ? 1 : 0.1)
        .opacity(popIn ? 1 : 0)
        .contentShape(Rectangle())
        .gesture(dragGesture)
        .simultaneousGesture(TapGesture().onEnded { toggleCollapse() })
        .help("单击展开 / 拖动移动")
    }
}

/// 聊天内容子视图：与面板拖动状态解耦（拖动/缩放 ghost 帧不重渲染重内容）。
/// 注意：不要给本视图加 .equatable()——它与 @ObservedObject + TextEditor 焦点有冲突，会导致输入框无法聚焦。
private struct PipChatContent: View {
    @ObservedObject var viewModel: ChatViewModel
    let loadError: String?
    let baseURL: String
    let token: String
    let compactFont: FontSizeManager

    var body: some View {
        ChatPage(chatViewModel: viewModel, loadError: loadError, baseURL: baseURL, token: token)
            .environmentObject(compactFont)
    }
}
#endif
