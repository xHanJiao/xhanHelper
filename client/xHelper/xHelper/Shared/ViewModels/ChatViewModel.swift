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
    @Published var currentToolCall: ToolCallData?
    @Published var thoughtMessage: String?
    @Published var statusMessage: String?
    @Published var errorMessage: String?
    @Published var conversationId: String?
    @Published var conversationTitle: String?
    @Published var deepMode = false
    @Published var lastMemoryIDs: [String]?
    @Published var showMemoryPopup = false

    @Published var planNodes: [PlanNode] = []
    @Published var isPlanModalVisible = false
    @Published var isPlanMinimized = false

    private let apiClient: APIClient
    private let sseClient = SSEClient()
    private var streamingContent = ""
    #if os(iOS)
    private var backgroundTaskID: UIBackgroundTaskIdentifier = .invalid
    #endif

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    // MARK: Load

    func loadConversation(id: String, title: String) async {
        conversationId = id
        conversationTitle = title
        do {
            messages = try await apiClient.getMessages(conversationId: id)
        } catch {
            errorMessage = "加载消息失败: \(error.localizedDescription)"
        }
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
        currentToolCall = nil
        thoughtMessage = nil
        statusMessage = nil
        errorMessage = nil
        planNodes = []
        isPlanModalVisible = false
        isPlanMinimized = false

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
        sseClient.connect(baseURL: baseURL, token: token, message: text, conversationId: conversationId, deepMode: deepMode, agentId: agentId)
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
            break

        case .message:
            if let delta = parseMessageDelta(event.data) {
                streamingContent += delta
                updateLastAssistantMessage(streamingContent)
            }

        case .status:
            statusMessage = parseStatus(event.data)

        case .toolCall:
            currentToolCall = parseToolCall(event.data)

        case .toolResult:
            if let tc = currentToolCall {
                let summary = buildToolSummary(toolCall: tc, resultJSON: event.data)
                let toolMsg = Message(
                    id: UUID().uuidString,
                    conversationId: conversationId ?? "",
                    role: "tool",
                    content: summary,
                    toolCallsJSON: nil,
                    createdAt: ISO8601DateFormatter().string(from: Date())
                )
                if let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) {
                    messages.insert(toolMsg, at: idx)
                } else {
                    messages.append(toolMsg)
                }
            }

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
            let tc = currentToolCall
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [self] in
                if currentToolCall?.function == tc?.function {
                    currentToolCall = nil
                }
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

    private func parseToolCall(_ data: String) -> ToolCallData? {
        guard let jsonData = data.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(ToolCallData.self, from: jsonData)
    }

    private func parseTitleFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["title"]
    }

    private func parseConversationIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["conversation_id"]
    }

    private func parseMessageIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["message_id"]
    }

    private func parseMemoryIDsFromDone(_ data: String) -> [String]? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let ids = obj["memory_ids"] as? [String]
        else { return nil }
        return ids
    }

    private func buildToolSummary(toolCall: ToolCallData, resultJSON: String) -> String {
        var parts: [String] = []
        parts.append(toolCall.displayName)
        for (key, val) in toolCall.argsSummary {
            parts.append("\(key): \(val)")
        }
        if let d = resultJSON.data(using: .utf8),
           let r = try? JSONDecoder().decode(ToolResultData.self, from: d) {
            parts.append(r.success ? "✅ 成功" : "❌ \(r.error ?? "失败")")
        }
        return parts.joined(separator: " | ")
    }
}
