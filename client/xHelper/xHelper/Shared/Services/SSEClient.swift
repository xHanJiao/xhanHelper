import Foundation

final class SSEClient: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private var session: URLSession!
    private var task: URLSessionDataTask?
    private var lineBuffer = ""
    private let lock = NSLock()

    var onEvent: ((StreamEvent) -> Void)?
    var onError: ((Error) -> Void)?

    override init() {
        super.init()
        session = URLSession(configuration: .default, delegate: self, delegateQueue: nil)
    }

    func connect(baseURL: String, token: String, message: String, conversationId: String?) {
        let url = URL(string: "\(baseURL)/api/chat")!
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

        var body: [String: String] = ["message": message]
        if let cid = conversationId { body["conversation_id"] = cid }
        req.httpBody = try? JSONEncoder().encode(body)

        task = session.dataTask(with: req)
        task?.resume()
    }

    func disconnect() {
        task?.cancel()
        task = nil
        lock.withLock { lineBuffer = "" }
    }

    // MARK: URLSessionDataDelegate

    nonisolated func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
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
        case "thought":     type = .thought
        case "status":      type = .status
        case "message":     type = .message
        case "tool_call":   type = .toolCall
        case "tool_result": type = .toolResult
        case "done":        type = .done
        case "error":       type = .error
        default:            return
        }
        onEvent?(StreamEvent(type: type, data: data))
    }
}
