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
    @Published var deepMode = false
    @Published var thinkingMode = true
    @Published var temperature: Double? = nil
    @Published var maxTokens: Int? = nil

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

    func loadConversation(id: String, title: String) async {
        sseClient.disconnect()
        isStreaming = false
        conversationId = id
        conversationTitle = title
        isInitialLoading = true
        errorMessage = nil
        loadMoreError = nil
        let oldLastId = messages.last?.id
        defer { isInitialLoading = false }
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
        isLoadingMore = true
        loadMoreError = nil
        let cursor = direction == "before" ? oldestCursor : newestCursor
        do {
            let result = try await apiClient.getMessagesCursor(
                conversationId: conversationId ?? "",
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
        sseClient.connect(baseURL: baseURL, token: token, message: text, conversationId: conversationId, deepMode: deepMode, agentId: agentId, thinking: thinkingMode, temperature: temperature, maxTokens: maxTokens, memoryThreshold: memoryThreshold, memoryLimit: memoryLimit, clientTools: clientTools)
    }

    func stopStreaming() {
        sseClient.disconnect()
        finishStreaming()
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
            }
            if let t = parseTitleFromDone(event.data) {
                conversationTitle = t
            }
            if let msgID = parseMessageIdFromDone(event.data) {
                replaceStreamingMessageID(with: msgID)
            }
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
        isStreaming = false
        if thoughtMessage != nil { thoughtDone = true }
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
