import Foundation
import OSLog

final class SSEClient: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private static let log = Logger(subsystem: "com.xhan.xhanHelper", category: "sseclient")

    private var session: URLSession!
    private var task: URLSessionDataTask?
    private var lineBuffer = ""
    private var httpStatusCode: Int?
    private let lock = NSLock()

    var onEvent: ((StreamEvent) -> Void)?
    var onError: ((Error) -> Void)?

    override init() {
        super.init()
        session = URLSession(configuration: .default, delegate: self, delegateQueue: nil)
    }

    func connect(baseURL: String, token: String, message: String, conversationId: String?, deepMode: Bool, agentId: String? = nil, thinking: Bool? = nil, temperature: Double? = nil, maxTokens: Int? = nil, memoryThreshold: Double = 0.5, memoryLimit: Int = 8, clientTools: [[String: Any]]? = nil, initMode: String? = nil, verifyEnabled: Bool? = nil, briefAnswer: Bool = false, hiddenRound: Bool = false, attachments: [String] = [], docQuote: DocQuote? = nil) {
        let url = URL(string: "\(baseURL)/api/chat")!
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

        var body: [String: Any] = ["message": message]
        if let cid = conversationId { body["conversation_id"] = cid }
        if deepMode { body["deep_mode"] = true }
        if let aid = agentId { body["agent_id"] = aid }
        if let t = thinking { body["thinking"] = t }
        if let temp = temperature { body["temperature"] = temp }
        if let mt = maxTokens { body["max_tokens"] = mt }
        if memoryThreshold != 0.5 { body["memory_threshold"] = memoryThreshold }
        if memoryLimit != 8 { body["memory_limit"] = memoryLimit }
        if let ct = clientTools, !ct.isEmpty { body["client_tools"] = ct }
        // 空 tab（无会话）场景：客户端已切换的模式经 init_mode 传给服务端，新建会话时落库
        if let im = initMode { body["init_mode"] = im }
        if let ve = verifyEnabled { body["verify_enabled"] = ve }
        // 精炼回答：开则服务端往 system prompt 注入「≤500 字」的输出约束（不走 max_tokens 硬截断）。
        // 会话级开关、每轮随请求发，与 verify_enabled 同族。
        if briefAnswer { body["brief_answer"] = true }
        // 隐藏轮（接力等控制轮）：本轮消息落库为隐藏状态，不进对话记录/LLM 上下文/检索/后处理
        if hiddenRound { body["hidden_round"] = true }
        // 附件图片：先经 POST /api/images/upload 上传拿到 image_id，这里只传 id 列表
        if !attachments.isEmpty { body["attachments"] = attachments }
        // 文档划线引用（「问 AI」）：会话级，引用还在就每轮带。只进 LLM 上下文（document_quote 块）
        if let q = docQuote, let data = try? JSONEncoder().encode(q),
           let obj = try? JSONSerialization.jsonObject(with: data) {
            body["doc_quote"] = obj
        }
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        if req.httpBody == nil {
            Self.log.error("failed to serialize request body")
        }

        lock.withLock {
            httpStatusCode = nil
            lineBuffer = ""
        }
        task = session.dataTask(with: req)
        task?.resume()
    }

    func disconnect() {
        task?.cancel()
        task = nil
        lock.withLock { lineBuffer = "" }
    }

    // MARK: URLSessionDataDelegate

    nonisolated func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        if let http = response as? HTTPURLResponse {
            lock.withLock { httpStatusCode = http.statusCode }
        }
        completionHandler(.allow)
    }

    nonisolated func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        // 非 2xx（如 404/500 的 JSON 错误体）不是 SSE 流，不喂给行解析器
        if let status = lock.withLock({ httpStatusCode }), !(200...299).contains(status) {
            return
        }
        guard let text = String(data: data, encoding: .utf8) else { return }
        lock.withLock { lineBuffer += text }
        processLines()
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if let error = error {
            let nsError = error as NSError
            if nsError.domain == NSURLErrorDomain && nsError.code == NSURLErrorCancelled {
                return
            }
            onError?(error)
            return
        }
        // 传输成功但 HTTP 非 2xx（无 error）：显式上报状态码，避免静默吞掉
        if let status = lock.withLock({ httpStatusCode }), !(200...299).contains(status) {
            let nsError = NSError(domain: "XHANHTTP", code: status, userInfo: [NSLocalizedDescriptionKey: "HTTP \(status)"])
            onError?(nsError)
        }
    }

    // MARK: Line parsing

    private func processLines() {
        lock.withLock {
            while let newlineIndex = lineBuffer.firstIndex(of: "\n") {
                let line = String(lineBuffer[..<newlineIndex])
                lineBuffer = String(lineBuffer[lineBuffer.index(after: newlineIndex)...])
                parseLine(line.trimmingCharacters(in: .whitespacesAndNewlines))
            }
        }
    }

    private var currentEvent = ""

    private func parseLine(_ line: String) {
        if line.hasPrefix("event: ") {
            currentEvent = String(line.dropFirst(7))
        } else if line.hasPrefix("data: ") {
            let data = String(line.dropFirst(6))
            dispatch(event: currentEvent, data: data)
            currentEvent = ""
        }
    }

    private func dispatch(event: String, data: String) {
        let type: StreamEventType
        switch event {
        case "plan":             type = .plan
        case "plan_node_start":  type = .planNodeStart
        case "plan_node_done":   type = .planNodeDone
        case "thought_delta":    type = .thoughtDelta
        case "thought_done":     type = .thoughtDone
        case "status":           type = .status
        case "verify":           type = .verify
        case "text_delta":       type = .message
        case "message":          type = .message
        case "tool_call":        type = .toolCall
        case "tool_result":      type = .toolResult
        case "client_tool_call": type = .clientToolCall
        case "subagent":         type = .subagent
        case "reminders_changed": type = .remindersChanged
        case "done":             type = .done
        case "error":            type = .error
        default:                 return
        }
        onEvent?(StreamEvent(type: type, data: data))
    }
}

