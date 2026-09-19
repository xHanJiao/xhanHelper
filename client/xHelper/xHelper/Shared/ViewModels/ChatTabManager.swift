import Combine
import Foundation

extension Notification.Name {
    /// 新会话在后端创建成功（空 tab 转正）后发出，会话列表应刷新置顶
    static let conversationListNeedsRefresh = Notification.Name("conversationListNeedsRefresh")
    /// 单个会话在服务端已消失（如无痕会话被销毁），列表应本地移除该条（保留分页）。
    /// userInfo["id"] = conversationId
    static let conversationRemoved = Notification.Name("conversationRemoved")
    /// 刷新发现一批会话已在服务端消失（无痕销毁），应关闭对应 tab。
    /// userInfo["ids"] = [conversationId]
    static let conversationsGone = Notification.Name("conversationsGone")
}

// MARK: - ChatTab

@MainActor
final class ChatTab: Identifiable, ObservableObject {
    let id: String
    var conversationId: String?
    @Published var title: String
    let viewModel: ChatViewModel
    var lastAccessedAt: Date

    /// 未产生任何对话的空 tab（conversationId 为空）
    var isBlank: Bool { conversationId == nil }

    init(id: String, conversationId: String?, title: String, viewModel: ChatViewModel) {
        self.id = id
        self.conversationId = conversationId
        self.title = title
        self.viewModel = viewModel
        self.lastAccessedAt = Date()
        if conversationId == nil {
            // 空 tab：置空字符串，发消息时后端创建非默认新会话
            viewModel.conversationId = ""
        }
    }
}

// MARK: - ChatTabManager

@MainActor
final class ChatTabManager: ObservableObject {
    @Published private(set) var tabs: [ChatTab] = []
    @Published private(set) var currentTabID: String?

    /// 会话历史（< > 导航）：访问顺序的 convID，无重复，最新在末尾
    @Published private(set) var history: [String] = []
    @Published private(set) var historyIndex: Int = -1

    /// 内存中同时保留的会话 tab 上限（空 tab 不占用，全局最多 1 个）
    let maxCachedTabs = 5

    private let makeViewModel: () -> ChatViewModel
    private var cancellables: Set<AnyCancellable> = []

    init(makeViewModel: @escaping () -> ChatViewModel) {
        self.makeViewModel = makeViewModel
        // 刷新发现无痕会话已在服务端销毁：关闭其 tab，让消息列表联动
        NotificationCenter.default.publisher(for: .conversationsGone)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] note in
                guard let ids = note.userInfo?["ids"] as? [String] else { return }
                Task { @MainActor in self?.handleConversationsGone(ids) }
            }
            .store(in: &cancellables)
    }

    var currentTab: ChatTab? {
        tabs.first { $0.id == currentTabID }
    }

    var currentConversationId: String? {
        currentTab?.conversationId
    }

    var canGoBack: Bool { historyIndex > 0 }
    var canGoForward: Bool { historyIndex >= 0 && historyIndex < history.count - 1 }

    /// 新建聊天：不发消息不在后端创建会话。空 tab 全局最多 1 个，
    /// 重复点击时重置/切换到已有空 tab。
    func newChat() {
        if let tab = currentTab, tab.isBlank {
            resetBlank(tab)
            return
        }
        if let existing = tabs.first(where: { $0.isBlank }) {
            activate(existing)
            resetBlank(existing)
            return
        }
        let vm = makeViewModel()
        let tab = ChatTab(id: UUID().uuidString, conversationId: nil, title: "新对话", viewModel: vm)
        vm.onConversationCreated = { [weak self] cid, title in
            self?.handleConversationCreated(cid: cid, title: title)
        }
        vm.onConversationGone = { [weak self] cid in
            self?.handleConversationGone(cid)
        }
        vm.onHandoffReady = { [weak self] title, id, baseURL, token in
            self?.startHandoffConversation(docTitle: title, docID: id, baseURL: baseURL, token: token)
        }
        vm.onOpenDocument = { id in
            DocumentSelectionCoordinator.shared.openDocument(id: id)
        }
        tabs.append(tab)
        activate(tab)
        resetBlank(tab)
    }

    /// 会话接力：新建会话并把「接力文档」作为首条消息发出，UI 切到新会话。
    func startHandoffConversation(docTitle: String, docID: String, baseURL: String, token: String) {
        newChat()
        guard let vm = currentTab?.viewModel else { return }
        vm.sendMessage(ChatViewModel.handoffTrigger(title: docTitle, id: docID), baseURL: baseURL, token: token)
    }

    /// 新建无痕会话：无痕标记必须在后端创建会话时落库，故先创建再打开。
    /// 与 newChat 不同——空 tab 无法携带无痕标记（首条消息才建会话）。
    func newEphemeralChat() async {
        do {
            let conv = try await makeViewModel().apiClient.createConversation(ephemeral: true)
            await openConversation(id: conv.id, title: conv.title, pushHistory: true)
            NotificationCenter.default.post(name: .conversationListNeedsRefresh, object: nil)
        } catch {
            LogBuffer.shared.error("ChatTabManager.newEphemeralChat: \(error)")
            ToastCenter.shared.show("创建无痕会话失败：\(error.localizedDescription)")
        }
    }

    /// 打开会话：缓存命中则切换恢复；未命中则新建 tab 并加载。切换时会销毁空 tab。
    /// pushHistory=true 表示用户主动切换，写入会话历史（< > 导航）；程序性切换（启动/提醒/回退驱动）传 false。
    func openConversation(id: String, title: String, pushHistory: Bool = false, mode: String? = nil) async {
        if let existing = tabs.first(where: { $0.conversationId == id }) {
            activate(existing)
            if pushHistory { pushHistoryEntry(id) }
            return
        }
        closeBlankTab()
        let vm = makeViewModel()
        vm.onConversationCreated = { [weak self] cid, title in
            self?.handleConversationCreated(cid: cid, title: title)
        }
        vm.onConversationGone = { [weak self] cid in
            self?.handleConversationGone(cid)
        }
        vm.onHandoffReady = { [weak self] title, id, baseURL, token in
            self?.startHandoffConversation(docTitle: title, docID: id, baseURL: baseURL, token: token)
        }
        vm.onOpenDocument = { id in
            DocumentSelectionCoordinator.shared.openDocument(id: id)
        }
        let tab = ChatTab(id: id, conversationId: id, title: title, viewModel: vm)
        tabs.append(tab)
        activate(tab)
        if pushHistory { pushHistoryEntry(id) }
        await vm.loadConversation(id: id, title: title, mode: mode)
        enforceCacheLimit()
    }

    /// 回退到上一个会话（< 按钮）；切换不入历史（防重入）
    func goBackHistory() async {
        guard canGoBack else { return }
        historyIndex -= 1
        await switchToHistoryEntry()
    }

    /// 前进到下一个会话（> 按钮）；切换不入历史（防重入）
    func goForwardHistory() async {
        guard canGoForward else { return }
        historyIndex += 1
        await switchToHistoryEntry()
    }

    /// 关闭指定会话 tab（删除会话后调用）。若关闭的是当前 tab，
    /// 自动切到最近访问的其他 tab，没有则开一个空 tab。
    func close(conversationId: String) {
        guard let idx = tabs.firstIndex(where: { $0.conversationId == conversationId }) else { return }
        let tab = tabs[idx]
        tab.viewModel.stopStreaming()
        tabs.remove(at: idx)
        // 从会话历史清理
        if let hidx = history.firstIndex(of: conversationId) {
            history.remove(at: hidx)
            if historyIndex > hidx { historyIndex -= 1 }
            if history.isEmpty { historyIndex = -1 }
        }
        if currentTabID == tab.id {
            if let next = tabs.sorted(by: { $0.lastAccessedAt > $1.lastAccessedAt }).first {
                activate(next)
            } else {
                currentTabID = nil
                newChat()
            }
        }
    }

    // MARK: - Private

    /// 写入历史：截断前进分支 + 去重（同会话移到栈顶）
    private func pushHistoryEntry(_ id: String) {
        if historyIndex < history.count - 1 {
            history.removeSubrange((historyIndex + 1)..<history.count)
        }
        history.removeAll { $0 == id }
        history.append(id)
        historyIndex = history.count - 1
    }

    /// 切换到历史当前位置对应的会话（不入历史）
    private func switchToHistoryEntry() async {
        guard historyIndex >= 0, historyIndex < history.count else { return }
        let id = history[historyIndex]
        // 软失效自愈：LRU 淘汰的会话会重新加载；删除的会话会被 close 清理不会出现在历史
        await openConversation(id: id, title: currentTab?.title ?? "", pushHistory: false)
    }

    private func activate(_ tab: ChatTab) {
        tab.lastAccessedAt = Date()
        currentTabID = tab.id
    }

    private func resetBlank(_ tab: ChatTab) {
        tab.viewModel.resetForNewChat()
        tab.title = "新对话"
        tab.lastAccessedAt = Date()
    }

    private func closeBlankTab() {
        guard let idx = tabs.firstIndex(where: { $0.isBlank }) else { return }
        let tab = tabs[idx]
        if currentTabID == tab.id {
            currentTabID = nil
        }
        tab.viewModel.stopStreaming()
        tabs.remove(at: idx)
    }

    private func handleConversationCreated(cid: String, title: String?) {
        guard let tab = currentTab, tab.isBlank else { return }
        tab.conversationId = cid
        if let t = title, !t.isEmpty {
            tab.title = t
        }
        NotificationCenter.default.post(name: .conversationListNeedsRefresh, object: nil)
    }

    /// 会话在服务端已消失（如无痕会话被销毁）：关闭对应 tab（复用 close 的自动切换逻辑）、
    /// 通知会话列表本地移除该条、提示用户。
    private func handleConversationGone(_ cid: String) {
        guard !cid.isEmpty else { return }
        LogBuffer.shared.warning("ChatTabManager: conversation gone, closing tab cid=\(cid)")
        close(conversationId: cid)
        NotificationCenter.default.post(name: .conversationRemoved, object: nil, userInfo: ["id": cid])
        ToastCenter.shared.show("会话不存在或已被销毁")
    }

    /// 批量：刷新发现无痕会话已销毁。仅关闭仍开着的 tab（已被 404 路径关掉的静默跳过，避免重复提示）。
    private func handleConversationsGone(_ ids: [String]) {
        let open = Set(tabs.compactMap { $0.conversationId })
        let hits = ids.filter { open.contains($0) }
        guard !hits.isEmpty else { return }
        LogBuffer.shared.warning("ChatTabManager: conversations gone, closing tabs: \(hits)")
        for id in hits {
            close(conversationId: id)
        }
        ToastCenter.shared.show("会话不存在或已被销毁")
    }

    private func enforceCacheLimit() {
        while tabs.count > maxCachedTabs {
            guard let victim = tabs.filter({ !$0.isBlank })
                .min(by: { $0.lastAccessedAt < $1.lastAccessedAt }),
                let idx = tabs.firstIndex(where: { $0.id == victim.id }),
                victim.id != currentTabID
            else { break }
            victim.viewModel.stopStreaming()
            tabs.remove(at: idx)
        }
    }
}
