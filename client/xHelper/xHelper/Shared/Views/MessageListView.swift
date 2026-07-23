import SwiftUI
import CoreImage.CIFilterBuiltins
#if os(iOS)
import UIKit
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
    let hasMoreBefore: Bool
    let isLoadingMore: Bool
    let onLoadMore: (String) -> Void
    let baseURL: String
    let token: String
    @State private var initialLoadDone = false
    @State private var activeTimeBubbleId: String? = nil
    @State private var autoDismissTask: Task<Void, Never>? = nil
    @State private var menuMessageId: String? = nil
    @State private var menuPoint: CGPoint = .zero
    @State private var touchGlobalPoint: CGPoint = .zero
    @State private var menuDismissTask: Task<Void, Never>? = nil
    @State private var menuShownAt: Date = .distantPast
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

    var body: some View {
        Group {
            if !messages.isEmpty {
                messageScrollView
            } else if isInitialLoading {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if errorMessage != nil {
                Color.clear
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Text("暂无消息")
                    .font(.callout)
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
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
            if isShareSelectMode {
                print("[ShareDebug] conversation changed, clearing select mode")
                exitShareSelectMode()
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                initialLoadDone = true
            }
        }
        .onChange(of: messages.count) { _, _ in
            if isShareSelectMode, !isStreaming {
                print("[ShareDebug] messages changed in select mode, refreshing snapshot (\(messagesSnapshot.count) → \(messages.count))")
                messagesSnapshot = messages
            }
            if !isStreaming { dismissMenu() }
        }
        .overlay { menuOverlay }
        .overlay(alignment: .bottom) {
            if isShareSelectMode { shareBottomBar }
        }
        .sheet(isPresented: $showShareResult) {
            ShareResultView(shareUrl: shareUrl, shareToken: shareToken)
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

                ForEach(messages.reversed()) { msg in
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
                        base
                            .simultaneousGesture(
                                DragGesture(minimumDistance: 0, coordinateSpace: .global)
                                    .onChanged { value in
                                        touchGlobalPoint = value.startLocation
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
                                            menuPoint = touchGlobalPoint
                                            menuShownAt = Date()
                                            menuMessageId = msg.id
                                            startMenuDismissTimer()
                                        }
                                    }
                            )
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
            if isStreaming || isShareSelectMode { return }
            let shouldShow = newY > viewHeight * 0.75
            if shouldShow != showScrollToBottom { showScrollToBottom = shouldShow }
        })
    }

    // MARK: Message row

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
            if msg.id == "__streaming__", let thought = thoughtMessage {
                ThinkingBubble(text: thought, isStreaming: !thoughtDone)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, Layout.chatHorizontalPadding)
            }

            if let cd = msg.chartData {
                ChartBubble(chartData: cd)
            } else if msg.role == "user" {
                ChatBubble(text: msg.content, isUser: true, messageId: msg.id, createdAt: msg.createdAt,
                           showTime: activeTimeBubbleId == msg.id,
                           onTapToShowTime: { toggleTimeBubble(msg.id) })
                    .allowsHitTesting(!isShareSelectMode)
            } else {
                ChatBubble(text: msg.content, isUser: false, messageId: msg.id, createdAt: msg.createdAt,
                           showTime: activeTimeBubbleId == msg.id,
                           onTapToShowTime: { toggleTimeBubble(msg.id) })
                    .allowsHitTesting(!isShareSelectMode)
            }
        }
    }

    // MARK: Share select indicator

    @ViewBuilder
    private func shareSelectIndicator(for msg: Message) -> some View {
        let isUnshareable = msg.chartData != nil || msg.localToolResult != nil || msg.id == "__streaming__"

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
        print("[ShareDebug] select mode ON — disabling menus, timebubble, scrollToBottom")
        messagesSnapshot = messages
        let rounds = buildShareableRounds(from: messagesSnapshot)
        let totalShareable = rounds.reduce(0) { $0 + $1.count }
        print("[ShareDebug] snapshot: \(messagesSnapshot.count) msgs, \(rounds.count) rounds, \(totalShareable) shareable")
        for (i, round) in rounds.enumerated() {
            let ids = round.map { "\($0.role.prefix(1)):\($0.id.prefix(8))" }.joined(separator: ", ")
            print("[ShareDebug] round[\(i)]: \(round.count) msgs — \(ids)")
        }
        selectedShareIds = []
        isShareSelectMode = true
    }

    private func exitShareSelectMode() {
        print("[ShareDebug] select mode OFF — enabling menus, timebubble, scrollToBottom")
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
            print("[ShareDebug] toggle round[\(roundIndex)]: remove, selected now=\(selectedShareIds.count)")
        } else {
            let roundUserCount = round.filter { $0.role == "user" }.count
            print("[ShareDebug] limit check: selected=\(selectedUserCount) + adding=\(roundUserCount) = \(selectedUserCount + roundUserCount)")
            if selectedUserCount + roundUserCount > 10 {
                ToastCenter.shared.show("最多选择 10 轮")
                print("[ShareDebug] limit reached: tried to add round[\(roundIndex)], already at max=\(selectedUserCount)/10")
                return
            }
            selectedShareIds.formUnion(roundIds)
            print("[ShareDebug] toggle round[\(roundIndex)]: add, selected now=\(selectedUserCount)")
        }
    }

    private func createShare() {
        guard let convId = conversationId else {
            print("[ShareDebug] API aborted: no conversationId")
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
                print("[ShareDebug] API success: token=\(result.token), expires=\(result.expiresAt)")
                await MainActor.run {
                    isCreatingShare = false
                    shareToken = result.token
                    #if DEBUG
                    shareUrl = "http://localhost:5173/s/messages/\(result.token)"
                    #else
                    shareUrl = "\(baseURL)/s/messages/\(result.token)"
                    #endif
                    showShareResult = true
                    exitShareSelectMode()
                }
            } catch {
                print("[ShareDebug] API failed: \(error)")
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
        if hasMoreBefore {
            HStack {
                if isLoadingMore {
                    ProgressView().scaleEffect(0.7)
                } else {
                    Image(systemName: "arrow.up")
                }
                Text(isLoadingMore ? "正在加载..." : "加载更早的消息")
                    .font(.caption)
            }
            .foregroundColor(Color("accentSecondary"))
            .onTapGesture {
                guard !isLoadingMore else { return }
                onLoadMore("before")
            }
            .onScrollVisibilityChange(threshold: 0.1) { isVisible in
                guard initialLoadDone, isVisible, !isLoadingMore else { return }
                onLoadMore("before")
            }
        } else {
            Text("已无更早消息")
                .font(.caption)
                .foregroundColor(.secondary)
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
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 20) {
            Text("分享链接已生成")
                .font(.headline)

            if let qrImage = generateQRCode(from: shareUrl) {
                #if os(iOS)
                Image(uiImage: qrImage)
                    .interpolation(.none)
                    .resizable()
                    .frame(width: 180, height: 180)
                #else
                Image(nsImage: qrImage)
                    .interpolation(.none)
                    .resizable()
                    .frame(width: 180, height: 180)
                #endif
            } else {
                RoundedRectangle(cornerRadius: 8)
                    .fill(Color.secondary.opacity(0.15))
                    .frame(width: 180, height: 180)
                    .overlay(Text("QR 生成失败").font(.caption).foregroundColor(.secondary))
            }

            Text(shareUrl)
                .font(.system(size: 12, design: .monospaced))
                .foregroundColor(.secondary)
                .lineLimit(2)
                .truncationMode(.middle)
                .padding(.horizontal, 20)

            HStack(spacing: 16) {
                Button {
                    copyToPasteboard(shareUrl)
                    ToastCenter.shared.show("链接已复制")
                    print("[ShareDebug] QR link copied: \(shareUrl)")
                } label: {
                    Label("拷贝链接", systemImage: "doc.on.doc")
                        .font(.subheadline)
                }
                .buttonStyle(.bordered)

                #if os(iOS)
                if let url = URL(string: shareUrl) {
                    ShareLink(item: url) {
                        Label("分享", systemImage: "square.and.arrow.up")
                            .font(.subheadline)
                    }
                    .buttonStyle(.bordered)
                }
                #else
                Button {
                    showMacShareMenu()
                } label: {
                    Label("分享", systemImage: "square.and.arrow.up")
                        .font(.subheadline)
                }
                .buttonStyle(.bordered)
                #endif
            }

            Button("关闭") {
                dismiss()
            }
            .buttonStyle(.plain)
            .font(.caption)
            .foregroundColor(.secondary)
        }
        .padding(24)
        .frame(minWidth: 300, minHeight: 420)
    }

    private func generateQRCode(from string: String) -> SharePlatformImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage else {
            print("[ShareDebug] QR failed: input length=\(string.count)")
            return nil
        }
        print("[ShareDebug] QR generated: \(Int(output.extent.width))x\(Int(output.extent.height)), correctionLevel=M")
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

    private func copyToPasteboard(_ text: String) {
        #if os(iOS)
        UIPasteboard.general.string = text
        #else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
    }

    #if os(macOS)
    private func showMacShareMenu() {
        guard let url = URL(string: shareUrl) else { return }
        let picker = NSSharingServicePicker(items: [url])
        if let window = NSApp.keyWindow, let contentView = window.contentView {
            let frame = NSRect(x: contentView.bounds.midX, y: contentView.bounds.midY, width: 1, height: 1)
            picker.show(relativeTo: frame, of: contentView, preferredEdge: .minY)
        }
        print("[ShareDebug] showing NSSharingServicePicker")
    }
    #endif
}
