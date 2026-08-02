import Combine
import Foundation
import SwiftUI
#if os(iOS)
import UIKit
#endif

// MARK: - Plan Node

struct PlanNode: Identifiable {
    let id: String
    var label: String
    var status: String // running | done | failed
}

// MARK: - Tool Confirmation（L3 工具执行前用户确认）

struct ToolConfirmation: Identifiable {
    let id = UUID()
    let toolName: String
    let displayName: String
    let argsSummary: String
    let continuation: CheckedContinuation<Bool, Never>
}

// MARK: - ChatViewModel

@MainActor
class ChatViewModel: ObservableObject {
    @Published var messages: [Message] = []
    @Published var isStreaming = false
    @Published var thoughtMessage: String?
    @Published var thoughtDone = false
    @Published var statusMessage: String?
    @Published var errorMessage: String?
    @Published var conversationId: String?
    @Published var conversationTitle: String?
    @Published var isEditing = false {
        didSet {
            print("[ChatVM] isEditing: \(isEditing)")
        }
    }
    @Published var deepMode = false
    @Published var thinkingMode = true
    @Published var temperature: Double? = nil
    @Published var maxTokens: Int? = nil
    @Published var draftText = ""

    /// 会话模式："work"（默认）| "plan"（只读讨论）。只能由用户切换（Tab 快捷键/胶囊条）。
    /// TODO: 会话重新加载时从服务端同步真实 mode（当前先重置为 work，需会话详情 API 完善）
    @Published var mode: String = "work" {
        didSet {
            print("[ChatVM] mode -> \(mode)")
        }
    }
    /// plan 模式展示名（只读讨论状态）
    var isPlanMode: Bool { mode == "plan" }

    /// 新建会话成功（空 tab 转正）时回调：(conversationId, title)
    var onConversationCreated: ((String, String?) -> Void)?

    private var lastHapticTime: Date = .distantPast

    @AppStorage("llm_temperature") static var cachedTemperature: Double = 1.0
    @AppStorage("llm_maxTokens") static var cachedMaxTokens: Int = 4096
    @AppStorage("llm_thinking") static var cachedThinking: Bool = true

    func applyLLMDefaults() {
        thinkingMode = Self.cachedThinking
        temperature = Self.cachedTemperature
        maxTokens = Self.cachedMaxTokens
    }

    func refreshAndCacheLLMConfig() async {
        guard let config = try? await apiClient.getLLMConfig() else {
            LogBuffer.shared.warning("ChatViewModel.refreshAndCacheLLMConfig: failed, using cached defaults")
            return
        }
        if let t = config.temperature { Self.cachedTemperature = t }
        if let m = config.maxTokens { Self.cachedMaxTokens = m }
        if let th = config.thinking { Self.cachedThinking = th }
        applyLLMDefaults()
    }
    @Published var lastMemoryIDs: [String]?
    @Published var lastNoteIDs: [String]?
    @Published var showMemoryPopup = false
    @Published var showNotePopup = false
    @Published var showChatSettings = false
    @AppStorage("memoryThreshold") var memoryThreshold: Double = 0.5
    @AppStorage("memoryLimit") var memoryLimit: Int = 8

    @Published var planNodes: [PlanNode] = []
    @Published var isPlanModalVisible = false
    @Published var isPlanMinimized = false

    /// 执行计划（todo，丑版展示）：goal + 步骤列表，对话结束/加载会话时刷新
    @Published var planGoal = ""
    @Published var planTodo: [PlanStep] = []

    /// 从服务端拉取当前会话的执行计划（丑版 UI 用，UI 优化后续）
    func loadPlanTodo() async {
        guard let cid = conversationId, !cid.isEmpty else { return }
        guard let plan = try? await apiClient.getConversationPlan(conversationId: cid) else { return }
        planGoal = plan.goal
        planTodo = plan.steps
    }

    /// L3 工具执行确认（同一时刻最多一个，client tool 顺序执行）
    @Published var pendingToolConfirmation: ToolConfirmation?
    private var confirmationResolved = false

    /// 信任模式（会话级本地状态）：开启后跳过 L3 工具确认，直接执行。
    /// 适合固定流程/重复操作；只影响客户端确认弹窗，服务端零感知。
    /// 换会话（loadConversation）自动关闭，安全默认。
    @Published var trustedMode = false

    /// 切换计划模式（work ↔ plan）。仅用户可触发（胶囊条点击）。
    /// 已有会话：PATCH 落库，失败回滚；空 tab（无会话）：只改本地，第一条消息经 init_mode 落库。
    func toggleMode() async {
        let next = isPlanMode ? "work" : "plan"
        guard let cid = conversationId, !cid.isEmpty else {
            mode = next // 空 tab：会话尚未创建，模式暂存本地，待首条消息 init_mode 落库
            return
        }
        let previous = mode
        mode = next
        do {
            try await apiClient.updateConversationMode(id: cid, mode: next)
            LogBuffer.shared.info("ChatViewModel.toggleMode: conv=\(cid) -> \(next)")
        } catch {
            LogBuffer.shared.warning("ChatViewModel.toggleMode failed: \(error)")
            mode = previous // 回滚
        }
    }

    /// 切换信任模式。仅用户可触发（胶囊条点击），切换时 Toast 明确反馈。
    func toggleTrustedMode() {
        trustedMode.toggle()
        if trustedMode {
            ToastCenter.shared.show("已进入信任模式：高风险工具不再确认")
        } else {
            ToastCenter.shared.show("已退出信任模式")
        }
    }

    /// 挂起等待用户确认 L3 工具执行；60s 超时视为拒绝
    func awaitToolConfirmation(toolName: String, arguments: [String: Any]) async -> Bool {
        // 信任模式：跳过确认直接放行
        if trustedMode {
            return true
        }
        let argsSummary = arguments.isEmpty
            ? "(无参数)"
            : String(arguments.map { "\($0.key)=\($0.value)" }.joined(separator: ", ").prefix(80))
        confirmationResolved = false
        return await withCheckedContinuation { (cont: CheckedContinuation<Bool, Never>) in
            let displayName = LocalToolRegistry.shared.displayName(for: toolName)
            pendingToolConfirmation = ToolConfirmation(toolName: toolName, displayName: displayName, argsSummary: argsSummary, continuation: cont)
            // 60s 超时自动拒绝（confirmationResolved 防双重 resume）
            Task { @MainActor [weak self] in
                try? await Task.sleep(for: .seconds(60))
                guard let self, !self.confirmationResolved else { return }
                self.confirmationResolved = true
                self.pendingToolConfirmation = nil
                cont.resume(returning: false)
            }
        }
    }

    /// 用户点击允许/拒绝，结束确认等待
    func resolveToolConfirmation(allow: Bool) {
        guard let conf = pendingToolConfirmation, !confirmationResolved else { return }
        confirmationResolved = true
        pendingToolConfirmation = nil
        conf.continuation.resume(returning: allow)
    }

    // 游标分页
    @Published var oldestCursor = ""
    @Published var newestCursor = ""
    @Published var hasMoreBefore = false
    @Published var hasMoreAfter = false
    @Published var isLoadingMore = false
    @Published var isInitialLoading = true
    @Published var loadMoreError: String?
    @Published var scrollToBottomRequested = false

    let apiClient: APIClient // 扩展（ChatViewModel+ClientTools）需要访问
    private let sseClient = SSEClient()
    private var streamingContent = ""
    private var throttleTimer: Timer?
    #if os(iOS)
    private var backgroundTaskID: UIBackgroundTaskIdentifier = .invalid
    #endif

    init(apiClient: APIClient) {
        self.apiClient = apiClient
        applyLLMDefaults()
    }

    // MARK: Load

    /// 加载会话。mode 从会话数据带入（服务端权威）；trustedMode 仅"切换会话"时重置，
    /// 同一会话刷新（scene 激活/重试/手动刷新）传 preserveTrustedMode=true 保持。
    func loadConversation(id: String, title: String, mode: String? = nil, preserveTrustedMode: Bool = false) async {
        sseClient.disconnect()
        isStreaming = false
        conversationId = id
        conversationTitle = title
        self.mode = mode ?? "work" // 会话列表数据带 mode（服务端权威）；旧数据缺失时默认 work
        if !preserveTrustedMode {
            trustedMode = false // 信任模式会话级：换会话自动关闭（安全默认）
        }
        planGoal = ""
        planTodo = []
        isInitialLoading = true
        errorMessage = nil
        loadMoreError = nil
        let oldLastId = messages.last?.id
        defer { isInitialLoading = false }
        Task { await self.loadPlanTodo() }
        do {
            let result = try await apiClient.getMessagesCursor(conversationId: id)
            messages = result.messages.flatMap { $0.withoutToolCalls() }
            if oldLastId != nil, oldLastId != messages.last?.id {
                scrollToBottomRequested = true
            }
            if let first = messages.first, let last = messages.last {
                await mergeLocalToolResults(from: first.createdAt, to: last.createdAt)
            }
            oldestCursor = result.oldestCursor
            newestCursor = result.newestCursor
            hasMoreBefore = result.hasMoreBefore
            hasMoreAfter = result.hasMoreAfter
        } catch {
            LogBuffer.shared.error("ChatViewModel.loadConversation: \(error)")
            errorMessage = "加载消息失败"
        }
    }

    func loadMore(direction: String = "before") async {
        guard !isLoadingMore else { return }
        guard let cid = conversationId, !cid.isEmpty else { return }
        isLoadingMore = true
        loadMoreError = nil
        let cursor = direction == "before" ? oldestCursor : newestCursor
        do {
            let result = try await apiClient.getMessagesCursor(
                conversationId: cid,
                cursor: cursor,
                direction: direction
            )
            if direction == "before" {
                let newMsgs = result.messages.flatMap { $0.withoutToolCalls() }
                messages = newMsgs + messages
                oldestCursor = result.oldestCursor
                let stillHasMore = result.hasMoreBefore
                DispatchQueue.main.async {
                    self.hasMoreBefore = stillHasMore
                }
                if let first = newMsgs.first, let last = messages.last {
                    Task { await mergeLocalToolResults(from: first.createdAt, to: last.createdAt) }
                }
            } else {
                messages = messages + result.messages.flatMap { $0.withoutToolCalls() }
                newestCursor = result.newestCursor
                hasMoreAfter = result.hasMoreAfter
            }
        } catch {
            LogBuffer.shared.error("ChatViewModel.loadMore: \(error)")
            loadMoreError = "加载失败，点击重试"
        }
        isLoadingMore = false
    }

    func loadMessages(_ msgs: [Message]) {
        messages = msgs
    }

    // MARK: Reset

    /// 重置为新聊天状态（不发消息不在后端创建会话，conversationId 置空字符串
    /// 使 SSE 携带 conversation_id="" 由后端创建非默认新会话）
    func resetForNewChat() {
        stopStreaming()
        messages = []
        isStreaming = false
        thoughtMessage = nil
        thoughtDone = false
        statusMessage = nil
        errorMessage = nil
        conversationId = ""
        conversationTitle = nil
        isEditing = false
        planNodes = []
        isPlanModalVisible = false
        isPlanMinimized = false
        lastMemoryIDs = nil
        lastNoteIDs = nil
        showMemoryPopup = false
        showNotePopup = false
        oldestCursor = ""
        newestCursor = ""
        hasMoreBefore = false
        hasMoreAfter = false
        isLoadingMore = false
        isInitialLoading = false
        loadMoreError = nil
        draftText = ""
        streamingContent = ""
    }

    // MARK: Send

    func sendMessage(_ text: String, baseURL: String, token: String, agentId: String? = nil) {
        let userMsg = Message.localUser(text: text, conversationId: conversationId ?? "")
        messages.append(userMsg)

        isStreaming = true
        streamingContent = ""
        thoughtMessage = nil
        thoughtDone = false
        statusMessage = nil
        errorMessage = nil
        planNodes = []
        isPlanModalVisible = false
        isPlanMinimized = false
        lastMemoryIDs = nil
        lastNoteIDs = nil

        messages.append(.streamingAssistant(conversationId: conversationId ?? ""))

        Task { @MainActor in
            let clientTools = await Self.collectClientTools()
            setupSSE(baseURL: baseURL, token: token, text: text, agentId: agentId, clientTools: clientTools)
        }
        #if os(iOS)
        beginBackgroundTask()
        #endif
    }

    private func setupSSE(baseURL: String, token: String, text: String, agentId: String? = nil, clientTools: [[String: Any]]? = nil) {
        sseClient.disconnect()
        sseClient.onEvent = { [weak self] event in
            Task { @MainActor in
                self?.handleEvent(event)
            }
        }
        sseClient.onError = { [weak self] error in
            Task { @MainActor in
                let nsError = error as NSError
                if nsError.domain == NSURLErrorDomain,
                   nsError.code == NSURLErrorNetworkConnectionLost || nsError.code == NSURLErrorCancelled {
                    self?.finishStreaming()
                    return
                }
                LogBuffer.shared.error("ChatViewModel.sse onError: \(error)")
                ToastCenter.shared.show(ToastCenter.friendlyErrorText(nsError, action: "发送"))
                self?.finishStreaming()
            }
        }
        // 空 tab（无会话）时带 init_mode：会话创建时应用客户端已切换的模式（work/plan）
        let initMode: String? = (conversationId == nil || conversationId?.isEmpty == true) ? mode : nil
        sseClient.connect(baseURL: baseURL, token: token, message: text, conversationId: conversationId, deepMode: deepMode, agentId: agentId, thinking: thinkingMode, temperature: temperature, maxTokens: maxTokens, memoryThreshold: memoryThreshold, memoryLimit: memoryLimit, clientTools: clientTools, initMode: initMode)
    }

    func stopStreaming() {
        // 先立即断开 SSE + UI 收尾（思考框消失），再后台通知服务端显式终止 ReAct。
        // 顺序保证：UI 即时响应，stopChat 失败/慢速不影响停止体验。
        let api = apiClient
        let cid = conversationId
        sseClient.disconnect()
        finishStreaming()
        Task {
            if let cid, !cid.isEmpty {
                do {
                    let stopped = try await api.stopChat(conversationId: cid)
                    LogBuffer.shared.info("stopChat: stopped=\(stopped)")
                } catch {
                    LogBuffer.shared.error("stopChat failed: \(error)")
                }
            }
        }
    }

    // MARK: SSE Event Handling

    private func handleEvent(_ event: StreamEvent) {
        switch event.type {
        case .planNodeStart:
            if let node = parsePlanNode(event.data) {
                if !isPlanModalVisible { isPlanModalVisible = true }
                if let idx = planNodes.firstIndex(where: { $0.id == node.id }) {
                    planNodes[idx] = node
                } else {
                    planNodes.append(node)
                }
            }

        case .planNodeDone:
            if let node = parsePlanNode(event.data) {
                if let idx = planNodes.firstIndex(where: { $0.id == node.id }) {
                    planNodes[idx] = node
                }
            }

        case .thought, .thoughtDelta:
            if thoughtMessage == nil { thoughtMessage = "" }
            thoughtMessage! += event.data

        case .thoughtDone:
            thoughtDone = true

        case .message:
            if let delta = parseMessageDelta(event.data) {
                streamingContent += delta
                throttleFlush()
                let now = Date()
                if now.timeIntervalSince(lastHapticTime) >= 0.15 {
                    #if os(iOS)
                    UIImpactFeedbackGenerator(style: .light).impactOccurred()
                    #endif
                    lastHapticTime = now
                }
            }

        case .status:
            statusMessage = parseStatus(event.data)

        case .toolCall:
            break

        case .toolResult:
            break

        case .remindersChanged:
            LogBuffer.shared.debug("[Reminder] SSE reminders_changed received")
            NotificationCenter.default.post(name: .remindersSyncTriggered, object: nil)

        case .clientToolCall:
            handleClientToolCall(event.data)

        case .done:
            finishStreaming()
            thoughtMessage = nil
            statusMessage = nil
            if let cid = parseConversationIdFromDone(event.data) {
                conversationId = cid
                onConversationCreated?(cid, parseTitleFromDone(event.data))
            }
            if let t = parseTitleFromDone(event.data) {
                conversationTitle = t
            }
            if let msgID = parseMessageIdFromDone(event.data) {
                replaceStreamingMessageID(with: msgID)
            }
            // 对话结束刷新执行计划（todo 可能被 plan.save 更新）
            Task { await self.loadPlanTodo() }
            if let mids = parseMemoryIDsFromDone(event.data), !mids.isEmpty {
                lastMemoryIDs = mids
            }
            if let nids = parseNoteIDsFromDone(event.data), !nids.isEmpty {
                lastNoteIDs = nids
            }
            // 任务完成，立即渐隐弹窗
            withAnimation(.easeOut(duration: 0.3)) {
                isPlanModalVisible = false
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { [self] in
                planNodes = []
            }

        case .error:
            LogBuffer.shared.error("ChatViewModel.sse error event: \(event.data)")
            ToastCenter.shared.show("服务出错：\(event.data)")
            finishStreaming()

        default:
            break
        }
    }

    // MARK: Streaming Lifecycle

    private func finishStreaming() {
        throttleTimer?.invalidate()
        throttleTimer = nil
        applyStreamingContent()
        // 停止/出错时若 __streaming__ 占位无内容，直接移除（防三个点动画残留）
        if let idx = messages.lastIndex(where: { $0.id == "__streaming__" }),
           messages[idx].content.isEmpty {
            messages.remove(at: idx)
        }
        isStreaming = false
        // 停止/出错路径无 done 事件，主动清思考状态避免思考框残留（正常 done 路径同样安全）
        thoughtMessage = nil
        thoughtDone = true
        #if os(iOS)
        endBackgroundTask()
        #endif
    }

    #if os(iOS)
    private func beginBackgroundTask() {
        backgroundTaskID = UIApplication.shared.beginBackgroundTask { [weak self] in
            self?.sseClient.disconnect()
            self?.endBackgroundTask()
            Task { @MainActor in
                self?.isStreaming = false
            }
        }
    }

    private func endBackgroundTask() {
        guard backgroundTaskID != .invalid else { return }
        UIApplication.shared.endBackgroundTask(backgroundTaskID)
        backgroundTaskID = .invalid
    }
    #endif

    // MARK: Plan Parsing

    private func parsePlanNode(_ data: String) -> PlanNode? {
        guard let d = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode(PlanNodeJSON.self, from: d)
        else { return nil }
        return PlanNode(id: obj.id, label: obj.label, status: obj.status)
    }

    private struct PlanNodeJSON: Codable {
        let id: String
        let label: String
        let status: String
    }

    // MARK: Helpers

    private func replaceStreamingMessageID(with msgID: String) {
        guard let index = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        var msg = messages[index]
        messages.remove(at: index)
        msg = Message(
            id: msgID,
            conversationId: msg.conversationId,
            role: msg.role,
            content: msg.content,
            toolCallsJSON: nil,
            createdAt: msg.createdAt
        )
        messages.insert(msg, at: index)
    }

    private func updateLastAssistantMessage(_ content: String) {
        guard let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        let last = messages[idx]
        messages[idx] = Message(
            id: "__streaming__",
            conversationId: last.conversationId,
            role: "assistant",
            content: content,
            toolCallsJSON: nil,
            createdAt: last.createdAt
        )
    }

    private func throttleFlush() {
        guard throttleTimer == nil else { return }
        throttleTimer = Timer.scheduledTimer(withTimeInterval: 0.1, repeats: false) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.throttleTimer = nil
                self?.applyStreamingContent()
            }
        }
    }

    private func applyStreamingContent() {
        let lastContent = messages.last(where: { $0.id == "__streaming__" })?.content
        guard lastContent != streamingContent else { return }
        updateLastAssistantMessage(streamingContent)
    }

    private func parseMessageDelta(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["delta"]
    }

    private func parseStatus(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["message"]
    }

    private func parseTitleFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        return obj["title"] as? String
    }

    private func parseConversationIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        return obj["conversation_id"] as? String
    }

    private func parseMessageIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        return obj["message_id"] as? String
    }

    private func parseMemoryIDsFromDone(_ data: String) -> [String]? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let ids = obj["memory_ids"] as? [String]
        else { return nil }
        return ids
    }

    private func parseNoteIDsFromDone(_ data: String) -> [String]? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let ids = obj["note_ids"] as? [String]
        else { return nil }
        return ids
    }

    // MARK: Local Tool Results

    func insertChartMessage(_ cd: ChartData) {
        let payload = (try? JSONEncoder().encode(cd)) ?? Data()
        var msg = Message(
            id: "chart_\(UUID().uuidString)",
            conversationId: conversationId ?? "",
            role: "local_tool_result",
            content: "",
            toolCallsJSON: nil,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
        msg.localToolResult = LocalToolResultData(type: .chart, payload: payload)
        msg.chartData = cd
        messages.append(msg)

        Task {
            await LocalToolResultStore.shared.upsert(type: .chart, payload: payload, message: msg)
        }
    }

    private func mergeLocalToolResults(from: String, to: String) async {
        let existingIDs = Set(messages.lazy.filter { $0.role == "local_tool_result" }.map { $0.id })
        let entries = await LocalToolResultStore.shared.entries(for: conversationId ?? "", from: from, to: to)

        for entry in entries where !existingIDs.contains(entry.id) {
            var msg = Message(
                id: entry.id,
                conversationId: conversationId ?? "",
                role: "local_tool_result",
                content: "",
                toolCallsJSON: nil,
                createdAt: entry.createdAt
            )
            msg.localToolResult = LocalToolResultData(type: entry.type, payload: entry.payload)
            if entry.type == .chart, let cd = try? JSONDecoder().decode(ChartData.self, from: entry.payload) {
                msg.chartData = cd
            }
            messages.append(msg)
        }

        messages.sort { $0.createdAt < $1.createdAt }

        for i in stride(from: messages.count - 1, through: 0, by: -1) {
            guard messages[i].role == "local_tool_result" else { continue }
            for j in i + 1 ..< messages.count {
                if messages[j].role == "assistant" {
                    let item = messages.remove(at: i)
                    let insertAt = j > i ? j : j + 1
                    messages.insert(item, at: min(insertAt, messages.count))
                    break
                }
            }
        }
    }
}
