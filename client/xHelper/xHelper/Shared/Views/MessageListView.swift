import SwiftUI
import CoreImage.CIFilterBuiltins
#if os(iOS)
import UIKit
import UniformTypeIdentifiers
#else
import AppKit
#endif

struct MessageListView: View {
    let messages: [Message]
    let isStreaming: Bool
    let isInitialLoading: Bool
    let errorMessage: String?
    let conversationId: String?
    let statusMessage: String?
    let thoughtMessage: String?
    let thoughtDone: Bool
    let toolQueue: [ToolCallDisplay]  // 工具调用展示队列（最新助手气泡下方 pill）
    let verifyStatus: VerifyStatus?   // 响应检验状态（最新助手气泡下方 pill）
    let onOpenDocument: (String) -> Void
    let hasMoreBefore: Bool
    let isLoadingMore: Bool
    let loadMoreError: String?
    let onLoadMore: (String) -> Void
    let onRetry: () -> Void
    let baseURL: String
    let token: String
    @State private var initialLoadDone = false
    @State private var activeTimeBubbleId: String? = nil
    @State private var autoDismissTask: Task<Void, Never>? = nil
    @State private var menuMessageId: String? = nil
    @State private var menuPoint: CGPoint = .zero
    @State private var menuFrames: [String: CGRect] = [:]
    @State private var menuDismissTask: Task<Void, Never>? = nil
    @State private var menuShownAt: Date = .distantPast
    /// 展开了的横向卡片条（键＝该组第一条消息 id）
    @State private var expandedCardStrips: Set<String> = []
    @State private var viewHeight: CGFloat = 0
    @Binding var scrollPosition: ScrollPosition
    @Binding var showScrollToBottom: Bool

    // MARK: Share state
    @State private var isShareSelectMode = false
    @State private var selectedShareIds: Set<String> = []
    @State private var messagesSnapshot: [Message] = []
    @State private var showShareResult = false
    @State private var shareToken: String = ""
    @State private var shareUrl: String = ""
    @State private var isCreatingShare = false

    // MARK: 调试面板
    @State private var debugTarget: MessageDebugTarget? = nil

    /// 消息区底部渐隐高度：内容从底部浮层（输入栏）下方穿过时，窗口最底缘收边用。
    /// 只作用于滚动正文，不波及本视图的 overlay（分享条/分享弹窗）。
    private let bottomFadeHeight: CGFloat = 24

    var body: some View {
        Group {
            if !messages.isEmpty {
                messageScrollView
                    // 底部渐隐：内容从底部浮层下方穿过，窗口最底缘会硬裁内容；最后
                    // bottomFadeHeight 淡出到透明（露出底图/材质），滚动时内容「溶入」。
                    // 挂在滚动正文上、位于各 .overlay 之前 → 分享条/分享弹窗不受影响。
                    .mask {
                        VStack(spacing: 0) {
                            Rectangle().fill(.black)
                            LinearGradient(colors: [.black, .clear],
                                           startPoint: .top, endPoint: .bottom)
                                .frame(height: bottomFadeHeight)
                        }
                    }
            } else {
                MinimumLoadingView(
                    isLoading: isInitialLoading,
                    errorMessage: errorMessage,
                    onRetry: onRetry
                ) {
                    Text("暂无消息")
                        .font(.callout)
                        .foregroundColor(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .background(
            GeometryReader { geo in
                Color.clear.onAppear {
                    viewHeight = geo.size.height
                }
                .onChange(of: geo.size.height) { _, h in
                    viewHeight = h
                }
            }
        )
        .onAppear {
            initialLoadDone = false
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                initialLoadDone = true
            }
        }
        .onChange(of: conversationId) { _, _ in
            initialLoadDone = false
            expandedCardStrips.removeAll()
            if isShareSelectMode {
                exitShareSelectMode()
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                initialLoadDone = true
            }
        }
        .onChange(of: messages.count) { _, _ in
            if isShareSelectMode, !isStreaming {
                messagesSnapshot = messages
            }
            if !isStreaming { dismissMenu() }
        }
        .overlay { menuOverlay }
        .overlay(alignment: .bottom) {
            if isShareSelectMode { shareBottomBar }
        }
        .overlay {
            if showShareResult {
                ZStack {
                    Color.black.opacity(0.2)
                        .ignoresSafeArea()
                        .onTapGesture { showShareResult = false }

                    ShareResultView(shareUrl: shareUrl, shareToken: shareToken)
                        #if os(iOS)
                        .frame(maxWidth: 320)
                        #else
                        .frame(maxWidth: 360)
                        #endif
                        .padding(24)
                        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 20))
                        .shadow(color: .black.opacity(0.15), radius: 20, y: 8)
                        .padding(24)
                }
                .transition(.scale(scale: 0.9).combined(with: .opacity))
            }
        }
        .animation(.easeInOut(duration: 0.25), value: showShareResult)
        .sheet(item: $debugTarget) { target in
            MessageDebugPanelView(baseURL: baseURL, token: token, messageID: target.id, onClose: { debugTarget = nil })
        }
    }

    // MARK: Menu overlay

    private var menuOverlay: some View {
        Group {
            if let menuId = menuMessageId {
                GeometryReader { overlayGeo in
                    let origin = overlayGeo.frame(in: .global).origin
                    let localX = menuPoint.x - origin.x
                    let localY = menuPoint.y - origin.y
                    VStack(spacing: 0) {
                        Button {
                            if let msg = messages.first(where: { $0.id == menuId }) {
                                copyToPasteboard(msg.content)
                            }
                            dismissMenu()
                        } label: {
                            Label("拷贝", systemImage: "doc.on.doc")
                                .font(.subheadline)
                        }
                        .buttonStyle(.plain)
                        .padding(.horizontal, 14).padding(.vertical, 10)

                        Divider()

                        Button {
                            dismissMenu()
                            enterShareSelectMode()
                        } label: {
                            Label("分享", systemImage: "square.and.arrow.up")
                                .font(.subheadline)
                        }
                        .buttonStyle(.plain)
                        .padding(.horizontal, 14).padding(.vertical, 10)
                    }
                    .fixedSize(horizontal: true, vertical: false)
                    .background(.regularMaterial)
                    .clipShape(RoundedRectangle(cornerRadius: 12))
                    .shadow(color: .black.opacity(0.15), radius: 8, y: 4)
                    .position(x: localX, y: localY)
                }
            }
        }
    }

    // MARK: ScrollView

    private var messageScrollView: some View {
        ScrollView {
            VStack(spacing: 18) {
                if let status = statusMessage {
                    HStack {
                        Image(systemName: "brain.head.profile")
                            .font(.caption)
                            .foregroundColor(.secondary)
                        Text(status)
                            .font(.caption)
                            .foregroundColor(.secondary)
                        Spacer()
                    }
                    .padding(.horizontal)
                    .scaleEffect(y: -1)
                }

                ForEach(displayRows.reversed()) { row in
                    switch row {
                    case .message(let msg):
                        messageRowWithGestures(msg)
                    case .cardStrip(let group):
                        cardStripRow(group)
                            .scaleEffect(y: -1)
                    }
                }
                loadMoreHeader
                    .scaleEffect(y: -1)
            }
            .padding(.vertical)
        }
        .scaleEffect(y: -1)
        .defaultScrollAnchor(.top)
        .scrollPosition($scrollPosition)
        .dismissKeyboardOnTap()
        .onScrollPhaseChange { _, newPhase in
            guard !isShareSelectMode else { return }
            if newPhase != .idle { dismissMenu() }
        }
        .simultaneousGesture(TapGesture().onEnded {
            guard !isShareSelectMode else { return }
            if Date().timeIntervalSince(menuShownAt) < 0.5 { return }
            dismissMenu()
        })
        .onScrollGeometryChange(for: CGFloat.self, of: { $0.contentOffset.y }, action: { _, newY in
            if isShareSelectMode { return }
            if isStreaming {
                // 流式期间只灭不亮：消息动态增长导致 contentOffset 有短暂抖动，
                // 放开亮灯会误触发且因后续逻辑无法回底灭灯（按钮常亮 bug）
                if newY <= viewHeight * 0.75, showScrollToBottom {
                    showScrollToBottom = false
                }
            } else {
                let shouldShow = newY > viewHeight * 0.75
                if shouldShow != showScrollToBottom { showScrollToBottom = shouldShow }
            }
        })
    }

    // MARK: Message row

    /// 显示行：普通消息一行；**相邻的卡片类本地产物**并成一行（横向卡片条）。
    /// 分享选择模式逐条（每张卡各自一条，跟分享那套指示器对齐），不分组。
    private enum DisplayRow: Identifiable {
        case message(Message)
        case cardStrip([Message])

        var id: String {
            switch self {
            case .message(let m): return m.id
            case .cardStrip(let g): return "strip_\(g.first?.id ?? "")"
            }
        }
    }

    private var displayRows: [DisplayRow] {
        guard !isShareSelectMode else { return messages.map { DisplayRow.message($0) } }
        return LocalArtifactGrouping.rows(messages) { $0.localArtifact?.isCardArtifact == true }.map { group in
            group.count == 1 ? .message(group[0]) : .cardStrip(group)
        }
    }

    /// 单条消息一行：分享态的选择圈 + 各端各自的菜单手势（原样，从滚动区里提出来）
    @ViewBuilder
    private func messageRowWithGestures(_ msg: Message) -> some View {
        let base = messageRow(msg)
            .scaleEffect(y: -1)
            .overlay {
                if isShareSelectMode, selectedShareIds.contains(msg.id), msg.role == "user" || msg.role == "assistant" {
                    Color.blue.opacity(0.06)
                        .allowsHitTesting(false)
                }
            }

        if isShareSelectMode {
            base.onTapGesture { toggleRound(for: msg) }
        } else {
            #if os(macOS)
            // 用户消息：无划词（无 NSTextView），右键整行任意位置弹我们的菜单；
            // 助手消息：纯原生划词 + 原生编辑菜单，不挂右键菜单（见 ChatBubble）
            if msg.role == "user" {
                base.contextMenu {
                    if !msg.content.isEmpty {
                        Button {
                            debugTarget = MessageDebugTarget(id: msg.id)
                        } label: {
                            Label("调试", systemImage: "ladybug.fill")
                        }
                        Button {
                            copyToPasteboard(msg.content)
                        } label: {
                            Label("拷贝", systemImage: "doc.on.doc")
                        }
                        Button {
                            enterShareSelectMode()
                        } label: {
                            Label("分享", systemImage: "square.and.arrow.up")
                        }
                    }
                }
            } else {
                base
            }
            #else
            base
                .background(
                    GeometryReader { geo in
                        Color.clear.onAppear {
                            menuFrames[msg.id] = geo.frame(in: .global)
                        }
                        .onChange(of: geo.frame(in: .global)) { _, f in
                            menuFrames[msg.id] = f
                        }
                    }
                )
                .simultaneousGesture(
                    LongPressGesture(minimumDuration: 0.5)
                        .onEnded { _ in
                            if !msg.content.isEmpty {
                                if menuMessageId == msg.id {
                                    dismissMenu()
                                    return
                                }
                                if let frame = menuFrames[msg.id], viewHeight > 0 {
                                    let screenW = UIScreen.main.bounds.width
                                    let menuX = min(max(frame.midX, 80), screenW - 80)
                                    let gap: CGFloat = 8
                                    if frame.midY > viewHeight * 0.5 {
                                        menuPoint = CGPoint(x: menuX, y: max(frame.minY - gap, 50))
                                    } else {
                                        menuPoint = CGPoint(x: menuX, y: min(frame.maxY + gap, viewHeight - 50))
                                    }
                                } else {
                                    menuPoint = CGPoint(x: UIScreen.main.bounds.midX, y: viewHeight * 0.35)
                                }
                                menuShownAt = Date()
                                menuMessageId = msg.id
                                startMenuDismissTimer()
                            }
                        }
                )
            #endif
        }
    }

    /// 一行的横向卡片条（≥2 张卡片类产物）。单张不进这里——走原来的 `localArtifactView`。
    private func cardStripRow(_ group: [Message]) -> some View {
        ArtifactCardStrip(items: group, expanded: cardStripExpandedBinding(group)) { msg in
            artifactCard(msg.localArtifact)
        }
        .padding(.horizontal, Layout.chatHorizontalPadding + 14)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// 展开态按「这一组的第一条消息 id」记，卡片增减/流式替换都不丢
    private func cardStripExpandedBinding(_ group: [Message]) -> Binding<Bool> {
        let key = group.first?.id ?? ""
        return Binding(
            get: { expandedCardStrips.contains(key) },
            set: { on in
                if on { expandedCardStrips.insert(key) } else { expandedCardStrips.remove(key) }
            }
        )
    }

    /// 卡片条里单张卡的渲染：尺寸必须与条子的 `cardWidth`/`cardHeight` 一致（条子按固定卡宽算排布）。
    /// 穷尽 switch —— 新增卡片类产物时编译器会在这里提醒补渲染。
    @ViewBuilder
    private func artifactCard(_ artifact: LocalArtifact?) -> some View {
        if let artifact {
            switch artifact {
            case .docRefs(let refs):
                if let ref = refs.first {
                    DocumentRefCard(ref: ref, width: ArtifactCardMetrics.width, height: ArtifactCardMetrics.height) {
                        onOpenDocument(ref.id)
                    }
                }
            case .chart:
                // chart 是 `isCardArtifact == false` 的全宽图表，永远不进条子
                EmptyView()
            }
        }
    }

    /// 只发图不写字时 content 为空：此时不渲染空气泡，只显示缩略图条。
    private func hasVisibleContent(_ msg: Message) -> Bool {
        !msg.content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    @ViewBuilder
    private func messageRow(_ msg: Message) -> some View {
        Group {
            if isShareSelectMode {
                HStack(alignment: .top, spacing: 10) {
                    shareSelectIndicator(for: msg)
                    messageRowContent(msg)
                }
                .padding(.horizontal, Layout.chatHorizontalPadding)
                .contentShape(Rectangle())
            } else {
                messageRowContent(msg)
            }
        }
    }

    @ViewBuilder
    private func messageRowContent(_ msg: Message) -> some View {
        VStack(spacing: 12) {
            // 思考块：挂在最后一条 assistant 消息上（流式时是 __streaming__，
            // done 后 id 替换为真实消息 id——仍是最后一条），结束后保留折叠态可展开查看
            if let thought = thoughtMessage, msg.role == "assistant", msg.id == messages.last?.id {
                ThinkingBubble(text: thought, isStreaming: !thoughtDone)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, Layout.chatHorizontalPadding)
            }

            // 工具调用 / 响应检验 pill：挂在正文上方（时序：思考 → 工具/校验 → 正文）
            if msg.id == "__streaming__" && (!toolQueue.isEmpty || verifyStatus != nil) {
                ToolActivityToast(toolQueue: toolQueue, verifyStatus: verifyStatus)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, Layout.chatHorizontalPadding)
            }

            if let artifact = msg.localArtifact {
                localArtifactView(artifact)
            } else if msg.role == "user" {
                // 附件图片：气泡上方，点开看大图
                if !msg.imageIds.isEmpty {
                    MessageAttachmentStrip(
                        imageIds: msg.imageIds,
                        baseURL: baseURL,
                        token: token,
                        localImage: msg.localImages.first
                    ) { image in
                        LightboxCenter.shared.present {
                            AttachmentPreviewView(image: image) { LightboxCenter.shared.dismiss() }
                        }
                    }
                    // 与气泡正文右缘对齐：气泡用的是 chatHorizontalPadding(24) + 正文内边距 14，
                    // 图片条原来只做 trailing 对齐（贴到 0），比文字多探出 38pt
                    .padding(.horizontal, Layout.chatHorizontalPadding + 14)
                    .frame(maxWidth: .infinity, alignment: .trailing)
                }
                if hasVisibleContent(msg) {
                    ChatBubble(text: msg.content, isUser: true, messageId: msg.id, createdAt: msg.createdAt,
                               showTime: activeTimeBubbleId == msg.id,
                               onTapToShowTime: { toggleTimeBubble(msg.id) })
                        .allowsHitTesting(!isShareSelectMode)
                }
            } else {
                ChatBubble(text: msg.content, isUser: false, messageId: msg.id, createdAt: msg.createdAt,
                           showTime: activeTimeBubbleId == msg.id,
                           onTapToShowTime: { toggleTimeBubble(msg.id) })
                    .allowsHitTesting(!isShareSelectMode)
            }
        }
    }

    /// 本地产物渲染分发：新增类型在这里加一个 case（编译器穷尽检查）。
    /// 文档卡片只在 macOS 出（iOS 无右栏文档面板）。
    @ViewBuilder
    private func localArtifactView(_ artifact: LocalArtifact) -> some View {
        switch artifact {
        case .chart(let cd):
            ChartBubble(chartData: cd)
        case .docRefs(let refs):
            #if os(macOS)
            if !refs.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(refs) { ref in
                        DocumentRefCard(ref: ref) { onOpenDocument(ref.id) }
                    }
                }
                .padding(.horizontal, Layout.chatHorizontalPadding + 14)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            #endif
        }
    }

    // MARK: Share select indicator
    @ViewBuilder
    private func shareSelectIndicator(for msg: Message) -> some View {
        let isUnshareable = msg.localToolResult != nil || msg.id == "__streaming__"

        if isUnshareable {
            VStack(spacing: 4) {
                Image(systemName: "info.circle")
                    .font(.caption)
                    .foregroundColor(.secondary.opacity(0.4))
                Text("不可分享")
                    .font(.system(size: 8))
                    .foregroundColor(.secondary.opacity(0.4))
            }
            .frame(width: 24)
            .padding(.top, 10)
        } else {
            let isSelected = selectedShareIds.contains(msg.id)
            ZStack {
                Circle()
                    .stroke(isSelected ? Color.blue : Color.secondary.opacity(0.4), lineWidth: 2)
                    .frame(width: 22, height: 22)
                if isSelected {
                    Circle()
                        .fill(Color.blue)
                        .frame(width: 22, height: 22)
                    Image(systemName: "checkmark")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.white)
                }
            }
            .frame(width: 24)
            .padding(.top, 10)
        }
    }

    // MARK: Share bottom bar

    private var shareBottomBar: some View {
        HStack {
            Button {
                exitShareSelectMode()
            } label: {
                Label("取消", systemImage: "xmark")
                    .font(.subheadline)
            }
            .buttonStyle(.plain)

            Spacer()

            Text("已选 \(selectedUserCount) 轮")
                .font(.subheadline)
                .foregroundColor(.secondary)

            Spacer()

            if isCreatingShare {
                ProgressView().scaleEffect(0.8)
            } else {
                Button("生成分享链接") {
                    createShare()
                }
                .font(.subheadline.weight(.medium))
                .disabled(selectedUserCount == 0)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        #if os(iOS)
        .padding(.bottom, 4)
        #endif
        .background(.ultraThinMaterial)
    }

    // MARK: Share logic

    private var selectedUserCount: Int {
        selectedShareIds.filter { id in
            messagesSnapshot.first(where: { $0.id == id })?.role == "user"
        }.count
    }

    private func buildShareableRounds(from msgs: [Message]) -> [[Message]] {
        let ordered = Array(msgs)
        var rounds: [[Message]] = []
        var current: [Message] = []
        for msg in ordered {
            if msg.id == "__streaming__" { continue }
            if msg.role == "user" {
                if !current.isEmpty { rounds.append(current) }
                current = [msg]
            } else if msg.role == "assistant" {
                current.append(msg)
            }
        }
        if !current.isEmpty { rounds.append(current) }
        return rounds
    }

    private func enterShareSelectMode() {
        messagesSnapshot = messages
        let rounds = buildShareableRounds(from: messagesSnapshot)
        let totalShareable = rounds.reduce(0) { $0 + $1.count }
        for (i, round) in rounds.enumerated() {
            let ids = round.map { "\($0.role.prefix(1)):\($0.id.prefix(8))" }.joined(separator: ", ")
        }
        selectedShareIds = []
        isShareSelectMode = true
    }

    private func exitShareSelectMode() {
        isShareSelectMode = false
        selectedShareIds = []
        messagesSnapshot = []
    }

    private func toggleRound(for msg: Message) {
        let rounds = buildShareableRounds(from: messagesSnapshot)
        guard let roundIndex = rounds.firstIndex(where: { $0.contains(where: { $0.id == msg.id }) }) else { return }
        let round = rounds[roundIndex]
        let roundIds = Set(round.filter { $0.role == "user" || $0.role == "assistant" }.map(\.id))

        let isRoundSelected = roundIds.isSubset(of: selectedShareIds)

        if isRoundSelected {
            selectedShareIds.subtract(roundIds)
        } else {
            let roundUserCount = round.filter { $0.role == "user" }.count
            if selectedUserCount + roundUserCount > 10 {
                ToastCenter.shared.show("最多选择 10 轮")
                return
            }
            selectedShareIds.formUnion(roundIds)
        }
    }

    private func createShare() {
        guard let convId = conversationId else {
            return
        }
        let userIds = selectedShareIds.filter { id in
            messagesSnapshot.first(where: { $0.id == id })?.role == "user"
        }
        guard !userIds.isEmpty else { return }
        guard userIds.count <= 10 else {
            ToastCenter.shared.show("最多选择 10 轮")
            return
        }

        isCreatingShare = true
        let api = APIClient(baseURL: baseURL, token: token)
        Task {
            do {
                let result = try await api.createMessageShare(conversationId: convId, userMessageIds: Array(userIds))
                await MainActor.run {
                    isCreatingShare = false
                    shareToken = result.token
                    // 分享链接指向前端源（本地开发 5173、线上同源），与后端 API 地址分离
                    shareUrl = "\(DeviceManager.webBaseURL)/s/messages/\(result.token)"
                    showShareResult = true
                    exitShareSelectMode()
                }
            } catch {
                await MainActor.run {
                    isCreatingShare = false
                    ToastCenter.shared.show("分享失败：\(error.localizedDescription)")
                }
            }
        }
    }

    // MARK: Existing helpers

    private func toggleTimeBubble(_ msgId: String) {
        if menuMessageId != nil {
            dismissMenu()
            return
        }
        autoDismissTask?.cancel()
        autoDismissTask = nil

        if activeTimeBubbleId == msgId {
            withAnimation(.easeInOut(duration: 0.2)) {
                activeTimeBubbleId = nil
            }
            return
        }

        withAnimation(.easeInOut(duration: 0.2)) {
            activeTimeBubbleId = msgId
        }

        autoDismissTask = Task {
            try? await Task.sleep(nanoseconds: 10_000_000_000)
            guard !Task.isCancelled else { return }
            await MainActor.run {
                withAnimation(.easeInOut(duration: 0.2)) {
                    activeTimeBubbleId = nil
                }
            }
        }
    }

    private func copyToPasteboard(_ text: String) {
        #if os(iOS)
        UIPasteboard.general.string = text
        #else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
    }

    private func dismissMenu() {
        menuDismissTask?.cancel()
        menuDismissTask = nil
        menuMessageId = nil
    }

    private func startMenuDismissTimer() {
        menuDismissTask?.cancel()
        menuDismissTask = Task {
            try? await Task.sleep(nanoseconds: 20_000_000_000)
            guard !Task.isCancelled else { return }
            await MainActor.run {
                menuMessageId = nil
            }
        }
    }

    @ViewBuilder
    private var loadMoreHeader: some View {
        LoadMoreHeader(
            isLoading: isLoadingMore,
            errorMessage: loadMoreError,
            hasMore: hasMoreBefore,
            onLoadMore: { onLoadMore("before") }
        )
        .onScrollVisibilityChange(threshold: 0.1) { isVisible in
            guard isVisible, hasMoreBefore, !isLoadingMore, loadMoreError == nil else { return }
            onLoadMore("before")
        }
    }
}

// MARK: - Share Result

#if os(iOS)
typealias SharePlatformImage = UIImage
#else
typealias SharePlatformImage = NSImage
#endif

struct ShareResultView: View {
    let shareUrl: String
    let shareToken: String

    var body: some View {
        let qrImage = generateQRCode(from: shareUrl)

        ViewThatFits(in: .vertical) {
            // Full: QR + caption + URL + buttons
            VStack(spacing: 14) {
                qrCard(qrImage)
                Text("扫码查看对话")
                    .font(.caption)
                    .foregroundColor(.secondary)
                urlBar
                buttonRow(qrImage)
            }

            // Compact: URL + buttons (no QR, no caption)
            VStack(spacing: 12) {
                urlBar
                buttonRow(qrImage)
            }
        }
        .padding(24)
        .frame(minWidth: 280)
    }

    // MARK: Sub-views

    private func qrCard(_ img: SharePlatformImage?) -> some View {
        Group {
            if let img = img {
                #if os(iOS)
                Image(uiImage: img)
                    .interpolation(.none)
                    .resizable()
                    .frame(width: 160, height: 160)
                #else
                Image(nsImage: img)
                    .interpolation(.none)
                    .resizable()
                    .frame(width: 160, height: 160)
                #endif
            } else {
                RoundedRectangle(cornerRadius: 8)
                    .fill(Color.secondary.opacity(0.15))
                    .frame(width: 160, height: 160)
                    .overlay(Text("QR 生成失败").font(.caption).foregroundColor(.secondary))
            }
        }
        .padding(16)
        .background(.white, in: RoundedRectangle(cornerRadius: 16))
        .shadow(color: .black.opacity(0.06), radius: 12, y: 6)
    }

    private var urlBar: some View {
        Button {
            copyURLToPasteboard(shareUrl)
            ToastCenter.shared.show("链接已复制")
        } label: {
            HStack(spacing: 8) {
                Image(systemName: "link")
                    .font(.caption)
                    .foregroundColor(.secondary)
                Text(shareUrl)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Image(systemName: "doc.on.doc")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            .padding(.horizontal, 14).padding(.vertical, 10)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
    }

    @ViewBuilder
    private func buttonRow(_ qrImage: SharePlatformImage?) -> some View {
        ViewThatFits(in: .horizontal) {
            // Wide: text buttons
            HStack(spacing: 12) {
                copyBtn(qrImage, compact: false)
                shareBtn(qrImage, compact: false)
            }

            // Narrow: icon-only
            HStack(spacing: 12) {
                copyBtn(qrImage, compact: true)
                shareBtn(qrImage, compact: true)
            }
        }
    }

    private func copyBtn(_ img: SharePlatformImage?, compact: Bool) -> some View {
        Button {
            if let img = img {
                copyQRToPasteboard(img)
            }
        } label: {
            if compact {
                Image(systemName: "doc.on.doc")
                    .font(.subheadline)
                    .foregroundColor(.primary)
            } else {
                Text("拷贝二维码")
                    .font(.subheadline)
                    .foregroundColor(.primary)
                    .frame(maxWidth: .infinity)
            }
        }
        .buttonStyle(.plain)
        .modifier(GlassCapsuleModifier())
    }

    @ViewBuilder
    private func shareBtn(_ img: SharePlatformImage?, compact: Bool) -> some View {
        #if os(iOS)
        if let img = img {
            ShareLink(item: ShareQRImage(image: img), preview: SharePreview("xhanHelper 分享", image: Image(uiImage: img))) {
                if compact {
                    Image(systemName: "square.and.arrow.up")
                        .font(.subheadline)
                        .foregroundColor(.primary)
                } else {
                    Text("分享二维码")
                        .font(.subheadline)
                        .foregroundColor(.primary)
                        .frame(maxWidth: .infinity)
                }
            }
            .buttonStyle(.plain)
            .modifier(GlassCapsuleModifier())
        }
        #else
        Button {
            if let img = img { showMacShareMenu(qrImage: img) }
        } label: {
            if compact {
                Image(systemName: "square.and.arrow.up")
                    .font(.subheadline)
                    .foregroundColor(.primary)
            } else {
                Text("分享二维码")
                    .font(.subheadline)
                    .foregroundColor(.primary)
                    .frame(maxWidth: .infinity)
            }
        }
        .buttonStyle(.plain)
        .modifier(GlassCapsuleModifier())
        #endif
    }

    private func generateQRCode(from string: String) -> SharePlatformImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage else {
            return nil
        }
        let scaled = output.transformed(by: CGAffineTransform(scaleX: 10, y: 10))
        #if os(iOS)
        let ctx = CIContext()
        guard let cg = ctx.createCGImage(scaled, from: scaled.extent) else { return nil }
        return UIImage(cgImage: cg)
        #else
        let rep = NSCIImageRep(ciImage: scaled)
        let img = NSImage(size: rep.size)
        img.addRepresentation(rep)
        return img
        #endif
    }

    private func copyURLToPasteboard(_ text: String) {
        #if os(iOS)
        UIPasteboard.general.string = text
        #else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
    }

    private func copyQRToPasteboard(_ img: SharePlatformImage) {
        #if os(iOS)
        UIPasteboard.general.image = img
        #else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setData(img.tiffRepresentation, forType: .tiff)
        #endif
        ToastCenter.shared.show("二维码已复制")
    }

    #if os(macOS)
    private func showMacShareMenu(qrImage: SharePlatformImage) {
        let picker = NSSharingServicePicker(items: [qrImage])
        if let window = NSApp.keyWindow, let contentView = window.contentView {
            let frame = NSRect(x: contentView.bounds.midX, y: contentView.bounds.midY, width: 1, height: 1)
            picker.show(relativeTo: frame, of: contentView, preferredEdge: .minY)
        }
    }
    #endif
}

#if os(iOS)
struct ShareQRImage: Transferable {
    let image: UIImage
    static var transferRepresentation: some TransferRepresentation {
        DataRepresentation(exportedContentType: .png) { item in
            item.image.pngData() ?? Data()
        }
    }
}
#endif

private struct GlassCapsuleModifier: ViewModifier {
    func body(content: Content) -> some View {
        content
            .padding(.horizontal, 16).padding(.vertical, 10)
            .background(.regularMaterial, in: Capsule())
    }
}
