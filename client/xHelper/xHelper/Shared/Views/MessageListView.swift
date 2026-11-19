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
    /// 流式中排队的消息：只渲染在列表底部，不混进 messages（done 按最后一条 user 回填真实 id）
    let pendingSends: [PendingSend]
    let pendingPaused: Bool
    let onDeletePending: (UUID) -> Void
    let onResumePending: () -> Void
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
    let onOpenArtifact: (String, String) -> Void
    let hasMoreBefore: Bool
    let isLoadingMore: Bool
    let loadMoreError: String?
    let onLoadMore: (String) -> Void
    let onRetry: () -> Void
    let baseURL: String
    let token: String
    /// 句级朗读进度（只给最后一条 assistant 消息，其余传 nil）
    let speech: SpeechProgress?
    @ObservedObject private var fontSizeManager = FontSizeManager.shared
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

    // MARK: Pending queue

    /// 流式中排队的消息：虚线气泡 + 「继续」入口。整组在倒序列表里翻转一次，
    /// 内部保持正常从上到下（与 statusMessage 同一套做法）。
    @ViewBuilder
    private var pendingQueueSection: some View {
        if !pendingSends.isEmpty {
            VStack(alignment: .trailing, spacing: 10) {
                HStack(spacing: 8) {
                    Rectangle()
                        .fill(Color.secondary.opacity(0.18))
                        .frame(height: 0.5)
                    Text(pendingPaused
                         ? "已暂停 · 上一条发送失败"
                         : "当前轮结束后依次发送 · \(pendingSends.count) 条")
                        .font(.system(size: 11.5))
                        .foregroundColor(pendingPaused ? .orange : .secondary)
                        .fixedSize()
                    if pendingPaused {
                        Button("继续发送") { onResumePending() }
                            .buttonStyle(.plain)
                            .font(.system(size: 11.5, weight: .semibold))
                            .foregroundColor(Color("appAccent"))
                    }
                    Rectangle()
                        .fill(Color.secondary.opacity(0.18))
                        .frame(height: 0.5)
                }
                .padding(.horizontal, Layout.chatHorizontalPadding)

                ForEach(pendingSends) { item in
                    pendingBubble(item)
                }
            }
            .scaleEffect(y: -1)
        }
    }

    @ViewBuilder
    private func pendingBubble(_ item: PendingSend) -> some View {
        HStack(alignment: .top, spacing: 0) {
            Spacer(minLength: 40)
            VStack(alignment: .leading, spacing: 4) {
                Text("排队中")
                    .font(.system(size: 10.5))
                    .foregroundColor(.secondary)
                if !item.text.isEmpty {
                    Text(item.text)
                        .font(.system(size: fontSizeManager.scaledSize))
                        .foregroundColor(Color("assistantText").opacity(0.66))
                        .fixedSize(horizontal: false, vertical: true)
                }
                if !item.attachments.isEmpty {
                    Text("\(item.attachments.count) 张图")
                        .font(.system(size: 10.5))
                        .foregroundColor(.secondary)
                }
                HStack {
                    Spacer(minLength: 0)
                    Button("删除") { onDeletePending(item.id) }
                        .buttonStyle(.plain)
                        .font(.system(size: 11))
                        .foregroundColor(.secondary)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 9)
            .background(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .strokeBorder(Color("assistantText").opacity(0.22),
                                  style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
            )
            .frame(maxWidth: 560, alignment: .trailing)
        }
        .padding(.horizontal, Layout.chatHorizontalPadding)
    }

    // MARK: ScrollView

    private var messageScrollView: some View {
        ScrollView {
            VStack(spacing: 18) {
                // 排队气泡：倒序列表的「最底部」，紧跟流式回复之后
                pendingQueueSection
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
                    case .cardSlot(let cards):
                        cardSlotRow(cards)
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

    /// 显示行：普通消息一行；**同一轮的卡片**并成一条横排槽（见 docs/design/html-artifact §9.9）。
    /// 分享选择模式逐条（每张卡各自一条，跟分享那套指示器对齐），不归组。
    private enum DisplayRow: Identifiable {
        case message(Message)
        case cardSlot([ChatCard])

        var id: String {
            switch self {
            case .message(let m): return m.id
            case .cardSlot(let cards): return "slot_\(cards.first?.id ?? "")"
            }
        }
    }

    /// 卡片槽里的一张卡。文档卡与 HTML 产物卡走两条数据路（独立消息 vs 消息字段），
    /// 但在这里统一成同一种"卡"，才能混排（§9.9）。
    private enum ChatCard: Identifiable {
        case html(ArtifactRef)
        case document(DocumentRef)

        var id: String {
            switch self {
            case .html(let ref): return Self.htmlKey(ref.id)
            case .document(let ref): return "doc:\(ref.id)"
            }
        }

        /// HTML 卡片的去重键（与 `id` 同源，供 `lastCardOwner` 构造键）。
        static func htmlKey(_ artifactID: String) -> String { "html:\(artifactID)" }

        static func cards(from artifact: LocalArtifact) -> [ChatCard] {
            if case .docRefs(let refs) = artifact { return refs.map { .document($0) } }
            return []
        }
    }

    /// 卡片 id → 最后一次出现的消息 id。
    /// 改版是原地覆盖（id 不变），但卡会挂到新一轮的消息上；只在最新一处显示，
    /// 否则同一页在聊天流里越改越多张（§9.9）。
    ///
    /// shortcut: 每次 body 重算都是 O(消息数)（流式期间每个 delta 一次）；几百条消息内够用，
    /// 真觉得卡了再提到 ViewModel 里按 messages 版本号缓存。
    private var lastCardOwner: [String: String] {
        var occurrences: [(cardID: String, messageID: String)] = []
        for msg in messages {
            if msg.role == "local_tool_result" {
                if let artifact = msg.localArtifact, artifact.isCardArtifact {
                    occurrences.append(contentsOf: ChatCard.cards(from: artifact).map {
                        (cardID: $0.id, messageID: msg.id)
                    })
                }
            } else if msg.role != "user" {
                occurrences.append(contentsOf: msg.artifactRefs.map {
                    (cardID: ChatCard.htmlKey($0.id), messageID: msg.id)
                })
            }
        }
        return ChatCardDedupe.lastOwner(occurrences)
    }

    private var displayRows: [DisplayRow] {
        guard !isShareSelectMode else { return messages.map { DisplayRow.message($0) } }
        #if os(macOS)
        // 一轮 = 一条 user 消息到该轮结束。本轮所有卡片（文档卡 / HTML 卡）收进同一个槽，
        // 槽放在**本轮开头**（user 消息之后、回复正文之前）：先给结果再给解释。
        // 流式期间卡片到达时槽在原地增长，正文往下让。
        var rows: [DisplayRow] = []
        var cards: [ChatCard] = []
        var roundStart: Int?
        let lastOwner = lastCardOwner
        func flush() {
            guard !cards.isEmpty else { return }
            // 按 id 去重：真撞了就是 SwiftUI 的 "ID occurs multiple times" + 可能错行，一行保险
            var seen = Set<String>()
            let slot = DisplayRow.cardSlot(cards.filter { seen.insert($0.id).inserted })
            if let start = roundStart {
                rows.insert(slot, at: start + 1)
            } else {
                // 没有本轮 user 消息（历史首段直接是助手消息）→ 只能放末尾
                rows.append(slot)
            }
            cards.removeAll()
        }
        for msg in messages {
            if msg.role == "user" {
                flush()
                rows.append(.message(msg))
                roundStart = rows.count - 1
                continue
            }
            if msg.role == "local_tool_result" {
                if let artifact = msg.localArtifact, artifact.isCardArtifact {
                    cards.append(contentsOf: ChatCard.cards(from: artifact).filter { lastOwner[$0.id] == msg.id })
                    continue
                }
                rows.append(.message(msg))
                continue
            }
            rows.append(.message(msg))
            cards.append(contentsOf: msg.artifactRefs
                .filter { lastOwner[ChatCard.htmlKey($0.id)] == msg.id }
                .map { .html($0) })
        }
        flush()
        return rows
        #else
        // **iOS 不归组**：无右栏、无文档卡，维持逐条渲染。同一页同样只留最新那条消息里的卡。
        let lastOwner = lastCardOwner
        return messages.map { msg in
            guard msg.role != "user" else { return DisplayRow.message(msg) }
            var filtered = msg
            filtered.artifacts = msg.artifactRefs.filter { lastOwner[ChatCard.htmlKey($0.id)] == msg.id }
            return .message(filtered)
        }
        #endif
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

    /// 卡片是否在消息内部内联渲染：macOS 平时由「本轮卡片槽」统一渲染（§9.9），
    /// 只有分享选择态要逐条可点时才回到内联。
    private var cardsRenderedInline: Bool {
        #if os(macOS)
        return isShareSelectMode
        #else
        return true
        #endif
    }

    /// 一行的卡片槽：本轮所有卡片（文档卡 / HTML 卡混排）。
    /// 单张不进条子——保持原样的整卡；≥2 张走 `ArtifactCardStrip` 的收起/展开那一套。
    @ViewBuilder
    private func cardSlotRow(_ cards: [ChatCard]) -> some View {
        if cards.count == 1, let card = cards.first {
            singleCard(card)
        } else {
            ArtifactCardStrip(items: cards, expanded: cardSlotExpandedBinding(cards)) { card in
                stripCard(card)
            }
            .padding(.horizontal, Layout.chatHorizontalPadding + 14)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// 单张卡：与改造前的观感一致（文档卡和产物卡本来就各有一个整卡样式）。
    @ViewBuilder
    private func singleCard(_ card: ChatCard) -> some View {
        switch card {
        case .html(let ref):
            HTMLArtifactCard(title: ref.title, summary: ref.summary) {
                onOpenArtifact(ref.id, ref.title)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            // 与正文/文档卡同一条左缘（正文是 chatHorizontalPadding + 14 的气泡内边距）
            .padding(.horizontal, Layout.chatHorizontalPadding + 14)
        case .document(let ref):
            DocumentRefCard(ref: ref) { onOpenDocument(ref.id) }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, Layout.chatHorizontalPadding + 14)
        }
    }

    /// 条子里的卡：尺寸必须与条子的 `cardWidth`/`cardHeight` 一致（条子按固定卡宽算排布）。
    /// 穷尽 switch —— 新增卡片类型时编译器会在这里提醒补渲染。
    @ViewBuilder
    private func stripCard(_ card: ChatCard) -> some View {
        switch card {
        case .html(let ref):
            HTMLArtifactCard(
                title: ref.title,
                summary: ref.summary,
                width: ArtifactCardMetrics.width,
                height: ArtifactCardMetrics.height
            ) {
                onOpenArtifact(ref.id, ref.title)
            }
        case .document(let ref):
            DocumentRefCard(
                ref: ref,
                width: ArtifactCardMetrics.width,
                height: ArtifactCardMetrics.height
            ) {
                onOpenDocument(ref.id)
            }
        }
    }

    /// 展开态按「这一槽的第一张卡 id」记。**槽首一变展开态就丢**（重排把别的卡插到队前、
    /// 或槽首那张被删），这是认了的行为——展开态本就只活 8s，不值得为它加一层稳定 id。
    private func cardSlotExpandedBinding(_ cards: [ChatCard]) -> Binding<Bool> {
        let key = cards.first?.id ?? ""
        return Binding(
            get: { expandedCardStrips.contains(key) },
            set: { on in
                if on { expandedCardStrips.insert(key) } else { expandedCardStrips.remove(key) }
            }
        )
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

            if !msg.artifactRefs.isEmpty, cardsRenderedInline {
                // HTML 产物卡片：数据来自消息字段（服务端），正文点开才拉
                ForEach(msg.artifactRefs) { ref in
                    HTMLArtifactCard(title: ref.title, summary: ref.summary) {
                        onOpenArtifact(ref.id, ref.title)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                // 与正文左缘对齐（正文泡内还有 14 的缩进）
                .padding(.horizontal, Layout.chatHorizontalPadding + 14)
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
                    ) { index in
                        LightboxCenter.shared.present {
                            ImageGalleryPreviewView(
                                source: .remote(msg.imageIds, baseURL: baseURL, token: token),
                                startIndex: index
                            ) { LightboxCenter.shared.dismiss() }
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
                           onTapToShowTime: { toggleTimeBubble(msg.id) },
                           speech: msg.id == messages.last?.id ? speech : nil)
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
            copyToPasteboard(shareUrl)
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
