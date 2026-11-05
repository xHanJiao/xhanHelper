import Foundation
import OSLog

final class SSEClient: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private static let log = Logger(subsystem: "com.xhan.xhanHelper", category: "sseclient")

    private var session: URLSession!
    private var lineDecoder = SSELineDecoder()
    private var warnedInvalidUTF8 = false
    /// 当前流的任务；delegate 只采纳它的回调（旧任务 cancel 后仍可能有已入队的回调）
    private var currentTask: URLSessionDataTask?
    private var httpStatusCode: Int?
    private var eventAssembler = SSEEventAssembler()
    private let lock = NSLock()

    /// 未知事件名（服务端加了新事件、客户端没跟上）：每种名字只警告一次，避免整轮刷屏。
    private let unknownEventLock = NSLock()
    private var warnedUnknownEvents = Set<String>()

    var onEvent: ((StreamEvent) -> Void)?
    var onError: ((Error) -> Void)?

    override init() {
        super.init()
        // SSE 是长连接：URLSession 默认 timeoutIntervalForRequest=60s（语义为「两次数据间隔」），
        // 审批/长工具执行期间若服务端心跳被丢，会先于服务端预算被掐断。显式放到 300s。
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 300
        session = URLSession(configuration: config, delegate: self, delegateQueue: nil)
    }

    func connect(baseURL: String, token: String, message: String, conversationId: String?, deepMode: Bool, agentId: String? = nil, thinking: Bool? = nil, temperature: Double? = nil, maxTokens: Int? = nil, clientTools: [[String: Any]]? = nil, initMode: String? = nil, verifyEnabled: Bool? = nil, briefAnswer: Bool = false, hiddenRound: Bool = false, attachments: [String] = [], ephemeral: Bool = false) {
        // 上一轮未断开时先取消；cancel() 只是尽力而为，已入队的回调仍会执行，
        // 由下面三个 delegate 里的 task 身份守卫拦掉
        disconnect()
        guard let url = URL(string: "\(baseURL)/api/chat") else {
            // 用户填的服务地址不合法（如残留的 `127.0.0.1:8080` 未归一化）：报错而不是崩。
            onError?(NSError(domain: "APIClient", code: -1000,
                userInfo: [NSLocalizedDescriptionKey: "服务地址无效：\(baseURL)"]))
            return
        }
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
        // 无痕：仅新建会话时生效（客户端空白 tab 开了无痕意图时随首条消息发）
        if ephemeral { body["ephemeral"] = true }
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        if req.httpBody == nil {
            Self.log.error("failed to serialize request body")
        }

        let newTask = session.dataTask(with: req)
        lock.withLock {
            // 与 delegate 的守卫同一把锁：只有当前任务的数据/完成回调会被采纳。
            // resume() 在锁外、这之后，所以这里清一次就够，旧状态不会在中间回流。
            currentTask = newTask
            httpStatusCode = nil
            lineDecoder.reset()
            warnedInvalidUTF8 = false
            eventAssembler.reset()
        }
        newTask.resume()
    }

    func disconnect() {
        // 先清 currentTask 再 cancel：cancel 只是尽力而为，先 cancel 会留下一段
        // 「已入队回调仍能通过守卫」的窗口。残留的一类情况是事件已 dispatch、
        // 在 Task { @MainActor } 里等着执行——那部分要按流代次（generation）才能根治。
        let stale: URLSessionDataTask? = lock.withLock {
            let t = currentTask
            currentTask = nil
            lineDecoder.reset()
            warnedInvalidUTF8 = false
            eventAssembler.reset()
            return t
        }
        stale?.cancel()
    }

    // MARK: URLSessionDataDelegate

    nonisolated func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        guard lock.withLock({ currentTask === dataTask }) else {
            completionHandler(.cancel)
            return
        }
        if let http = response as? HTTPURLResponse {
            lock.withLock { httpStatusCode = http.statusCode }
        }
        completionHandler(.allow)
    }

    nonisolated func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        // 旧任务的残留数据不能进新流的解码器
        guard lock.withLock({ currentTask === dataTask }) else { return }
        // 非 2xx（如 404/500 的 JSON 错误体）不是 SSE 流，不喂给行解析器
        if let status = lock.withLock({ httpStatusCode }), !(200...299).contains(status) {
            return
        }
        var invalidUTF8 = false
        lock.withLock {
            // 按字节缓冲到完整行再解码：块边界可能切在多字节字符中间，
            // 逐块解码会整块丢弃（曾静默吞掉 client_tool_call，见 SSELineDecoder 注释）
            let (lines, hadInvalidUTF8) = lineDecoder.append(data)
            if hadInvalidUTF8 && !warnedInvalidUTF8 {
                warnedInvalidUTF8 = true
                invalidUTF8 = true
            }
            for line in lines {
                parseLine(line.trimmingCharacters(in: .whitespacesAndNewlines))
            }
        }
        // 锁外打日志；每条流只报第一次，避免工具输出回灌时刷屏
        if invalidUTF8 {
            Self.log.warning("SSE line contained invalid UTF-8; replaced bad bytes instead of dropping the chunk")
        }
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        // 旧任务的完成回调会把新消息的流式气泡 finishStreaming 掉，必须拦
        guard lock.withLock({ currentTask === task }) else { return }
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

    private func parseLine(_ line: String) {
        // 不在这里直接派发：一条事件可能有多行 `data:`（payload 里的换行由服务端拆开）
        guard let event = eventAssembler.feed(line) else { return }
        dispatch(event: event.event, data: event.data)
    }

    private func dispatch(event: String, data: String) {
        guard let type = StreamEventType.from(eventName: event) else {
            // 注意：本方法在 `lock` 内被调用（见 didReceive data），这里必须用独立的锁。
            let first = unknownEventLock.withLock { warnedUnknownEvents.insert(event).inserted }
            if first {
                Self.log.warning("unknown SSE event (server/client drift): \(event, privacy: .public)")
            }
            return
        }
        onEvent?(StreamEvent(type: type, data: data))
    }
}

