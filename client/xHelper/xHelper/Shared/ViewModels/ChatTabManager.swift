import Combine
import Foundation

extension Notification.Name {
    /// 新会话在后端创建成功（空 tab 转正）后发出，会话列表应刷新置顶
    static let conversationListNeedsRefresh = Notification.Name("conversationListNeedsRefresh")
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

    init(makeViewModel: @escaping () -> ChatViewModel) {
        self.makeViewModel = makeViewModel
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
        tabs.append(tab)
        activate(tab)
        resetBlank(tab)
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
