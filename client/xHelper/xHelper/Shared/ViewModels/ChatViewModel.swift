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

    private let apiClient: APIClient
    private let sseClient = SSEClient()
    private var streamingContent = ""
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
        defer { isInitialLoading = false }
        do {
            let result = try await apiClient.getMessagesCursor(conversationId: id)
            messages = result.messages.flatMap { $0.withoutToolCalls() }
            oldestCursor = result.oldestCursor
            newestCursor = result.newestCursor
            hasMoreBefore = result.hasMoreBefore
            hasMoreAfter = result.hasMoreAfter
        } catch {
            LogBuffer.shared.error("ChatViewModel.loadConversation: \(error)")
            errorMessage = "加载消息失败: \(error.localizedDescription)"
        }
    }

    func loadMore(direction: String = "before") async {
        guard !isLoadingMore else { return }
        isLoadingMore = true
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
            } else {
                messages = messages + result.messages.flatMap { $0.withoutToolCalls() }
                newestCursor = result.newestCursor
                hasMoreAfter = result.hasMoreAfter
            }
        } catch {
            LogBuffer.shared.error("ChatViewModel.loadMore: \(error)")
            errorMessage = "加载更多失败: \(error.localizedDescription)"
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

        setupSSE(baseURL: baseURL, token: token, text: text, agentId: agentId)
        #if os(iOS)
        beginBackgroundTask()
        #endif
    }

    private func setupSSE(baseURL: String, token: String, text: String, agentId: String? = nil) {
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
                self?.errorMessage = error.localizedDescription
                self?.finishStreaming()
            }
        }
        sseClient.connect(baseURL: baseURL, token: token, message: text, conversationId: conversationId, deepMode: deepMode, agentId: agentId, thinking: thinkingMode, temperature: temperature, maxTokens: maxTokens, memoryThreshold: memoryThreshold, memoryLimit: memoryLimit)
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
                updateLastAssistantMessage(streamingContent)
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
            errorMessage = event.data
            finishStreaming()

        default:
            break
        }
    }

    // MARK: Streaming Lifecycle

    private func finishStreaming() {
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
        guard var last = messages.last, last.id == "__streaming__" else { return }
        messages.removeLast()
        last = Message(
            id: "__streaming__",
            conversationId: last.conversationId,
            role: "assistant",
            content: content,
            toolCallsJSON: nil,
            createdAt: last.createdAt
        )
        messages.append(last)
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
}
