import Combine
import Foundation
import SwiftUI

@MainActor
class ChatViewModel: ObservableObject {
    @Published var messages: [Message] = []
    @Published var isStreaming = false
    @Published var currentToolCall: ToolCallData?
    @Published var errorMessage: String?
    @Published var conversationId: String?

    private let apiClient: APIClient
    private let sseClient = SSEClient()
    private var streamingContent = ""

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    // MARK: Load

    func loadConversation(id: String) async {
        conversationId = id
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

    func sendMessage(_ text: String, baseURL: String, token: String) {
        let userMsg = Message.localUser(text: text, conversationId: conversationId ?? "")
        messages.append(userMsg)

        isStreaming = true
        streamingContent = ""
        currentToolCall = nil
        errorMessage = nil

        // 空 assistant 消息占位
        messages.append(.streamingAssistant(conversationId: conversationId ?? ""))

        setupSSE(baseURL: baseURL, token: token, text: text)
    }

    private func setupSSE(baseURL: String, token: String, text: String) {
        sseClient.disconnect()  // 先断开上一轮连接
        sseClient.onEvent = { [weak self] event in
            Task { @MainActor in
                self?.handleEvent(event)
            }
        }
        sseClient.onError = { [weak self] error in
            Task { @MainActor in
                self?.errorMessage = error.localizedDescription
                self?.isStreaming = false
            }
        }
        sseClient.connect(baseURL: baseURL, token: token, message: text, conversationId: conversationId)
    }

    func stopStreaming() {
        sseClient.disconnect()
        isStreaming = false
    }

    // MARK: SSE Event Handling

    private func handleEvent(_ event: StreamEvent) {
        switch event.type {
        case .message:
            if let delta = parseMessageDelta(event.data) {
                streamingContent += delta
                updateLastAssistantMessage(streamingContent)
            }

        case .toolCall:
            currentToolCall = parseToolCall(event.data)

        case .toolResult:
            // 插入永久工具记录到消息列表
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
                // 插入到 streaming assistant 消息之前
                if let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) {
                    messages.insert(toolMsg, at: idx)
                } else {
                    messages.append(toolMsg)
                }
            }

        case .done:
            isStreaming = false
            currentToolCall = nil
            if let cid = parseConversationIdFromDone(event.data) {
                conversationId = cid
            }
            if let msgID = parseMessageIdFromDone(event.data) {
                replaceStreamingMessageID(with: msgID)
            }

        case .error:
            errorMessage = event.data
            isStreaming = false
        }
    }

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

    // MARK: Parsers

    private func parseMessageDelta(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["delta"]
    }

    private func parseToolCall(_ data: String) -> ToolCallData? {
        guard let jsonData = data.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(ToolCallData.self, from: jsonData)
    }

    private func parseConversationIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["conversation_id"]
    }

    private func buildToolSummary(toolCall: ToolCallData, resultJSON: String) -> String {
        var parts: [String] = []
        parts.append(toolCall.displayName)
        for (key, val) in toolCall.argsSummary {
            parts.append("\(key): \(val)")
        }
        // 解析 tool_result 状态
        if let d = resultJSON.data(using: .utf8),
           let r = try? JSONDecoder().decode(ToolResultData.self, from: d) {
            parts.append(r.success ? "✅ 成功" : "❌ \(r.error ?? "失败")")
        }
        return parts.joined(separator: " | ")
    }

    private func parseMessageIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["message_id"]
    }
}
