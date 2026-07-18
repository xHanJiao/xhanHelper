#if os(macOS)
import Foundation

enum MCPServerState {
    case stopped
    case starting
    case ready
    case error
}

// MCPServer — 单个 MCP 子进程封装。
// JSON-RPC 2.0 over stdio（newline-delimited），pending 路由用
// CheckedContinuation（等价 Go 的 pending map + channel）。
// 生命周期由 MCPManager（actor）串行驱动；readLoop 回调线程只经 lock 触碰 pending。
final class MCPServer {
    let config: MCPServerConfig
    private(set) var state: MCPServerState = .stopped
    private(set) var toolsCache: [MCPToolDef] = []
    private(set) var lastUsedAt = Date()
    private(set) var inFlight = 0

    private var process: Process?
    private var stdinPipe: Pipe?
    private var stdoutPipe: Pipe?
    private var buffer = Data()
    private var nextID: Int64 = 0
    private var pending: [Int64: CheckedContinuation<JSONRPCResponse, Error>] = [:]
    private let lock = NSLock()
    private var stopping = false

    init(config: MCPServerConfig) {
        self.config = config
    }

    // MARK: - 启动 + MCP 握手

    func start() async throws {
        state = .starting
        stopping = false

        let p = Process()
        // GUI App 的 PATH 不含 homebrew 路径，用 /usr/bin/env + 增广 PATH 解析命令
        var env = ProcessInfo.processInfo.environment
        let extraPath = "/opt/homebrew/bin:/usr/local/bin"
        env["PATH"] = extraPath + ":" + (env["PATH"] ?? "/usr/bin:/bin")
        if let custom = config.env {
            env.merge(custom) { _, new in new }
        }
        p.environment = env
        p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        p.arguments = [config.command] + config.args

        let inPipe = Pipe()
        let outPipe = Pipe()
        p.standardInput = inPipe
        p.standardOutput = outPipe
        p.standardError = FileHandle.nullDevice

        outPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            if data.isEmpty {
                self?.handleEOF()
            } else {
                self?.handleData(data)
            }
        }
        p.terminationHandler = { [weak self] _ in
            guard let self, !self.stopping else { return }
            MCPLog.error("[MCP] \(self.config.name) process exited unexpectedly")
            self.failAllPending(MCPError(message: "MCP 进程已退出"))
            self.state = .error
        }

        process = p
        stdinPipe = inPipe
        stdoutPipe = outPipe

        do {
            try p.run()
        } catch {
            state = .error
            throw MCPError(message: "启动 \(config.command) 失败: \(error.localizedDescription)")
        }

        // MCP 握手：initialize → initialized 通知 → tools/list
        let initResp = try await call(method: "initialize", params: [
            "protocolVersion": "2024-11-05",
            "capabilities": [String: Any](),
            "clientInfo": ["name": "xhanHelper", "version": "0.1.0"],
        ], timeoutSecs: 15)
        if initResp.isError {
            state = .error
            throw MCPError(message: "initialize 失败: \(initResp.errorMessage ?? "unknown")")
        }
        try send(JSONRPC.notificationData(method: "notifications/initialized"))

        let listResp = try await call(method: "tools/list", timeoutSecs: 15)
        if listResp.isError {
            state = .error
            throw MCPError(message: "tools/list 失败: \(listResp.errorMessage ?? "unknown")")
        }
        toolsCache = MCPToolDef.parseList(from: listResp.result)
        lastUsedAt = Date()
        state = .ready
        MCPLog.info("[MCP] \(config.name) ready, \(toolsCache.count) tools")
    }

    // MARK: - 停止

    func stop() {
        guard let p = process else { return }
        stopping = true
        state = .stopped
        stdoutPipe?.fileHandleForReading.readabilityHandler = nil
        failAllPending(MCPError(message: "MCP server 已停止"))

        p.terminate() // SIGTERM
        let pid = p.processIdentifier
        DispatchQueue.global().asyncAfter(deadline: .now() + 5) {
            if p.isRunning {
                kill(pid, SIGKILL)
            }
        }
        process = nil
        stdinPipe = nil
        stdoutPipe = nil
        MCPLog.info("[MCP] \(config.name) stopped")
    }

    // MARK: - 工具调用

    // 返回 tools/call 的 result 原始 JSON（Data）。
    // 超时 55s：比服务端 60s 略小，保证客户端先回传明确错误，而非双方同时超时竞态。
    func callTool(name: String, args: [String: Any], timeoutSecs: TimeInterval = 55) async throws -> Data {
        lock.lock()
        inFlight += 1
        lock.unlock()
        defer {
            lock.lock()
            inFlight -= 1
            lastUsedAt = Date()
            lock.unlock()
        }

        let resp = try await call(method: "tools/call", params: [
            "name": name,
            "arguments": args,
        ], timeoutSecs: timeoutSecs)
        if resp.isError {
            throw MCPError(message: resp.errorMessage ?? "tools/call 失败")
        }
        guard let result = resp.result else {
            return Data("{}".utf8)
        }
        return try JSONSerialization.data(withJSONObject: result)
    }

    // MARK: - JSON-RPC call（pending + continuation）

    private func call(method: String, params: [String: Any]? = nil, timeoutSecs: TimeInterval) async throws -> JSONRPCResponse {
        lock.lock()
        nextID += 1
        let id = nextID
        lock.unlock()

        return try await withCheckedThrowingContinuation { cont in
            lock.lock()
            pending[id] = cont
            lock.unlock()

            do {
                try send(JSONRPC.requestData(id: id, method: method, params: params))
            } catch {
                lock.lock()
                let c = pending.removeValue(forKey: id)
                lock.unlock()
                c?.resume(throwing: error)
                return
            }

            // 超时兜底
            DispatchQueue.global().asyncAfter(deadline: .now() + timeoutSecs) { [weak self] in
                guard let self else { return }
                self.lock.lock()
                let c = self.pending.removeValue(forKey: id)
                self.lock.unlock()
                c?.resume(throwing: MCPError(message: "\(method) 超时（\(Int(timeoutSecs))s）"))
            }
        }
    }

    private func send(_ data: Data) throws {
        guard let fh = stdinPipe?.fileHandleForWriting else {
            throw MCPError(message: "MCP 进程未运行")
        }
        try fh.write(contentsOf: data)
    }

    // MARK: - readLoop

    private func handleData(_ data: Data) {
        buffer.append(data)
        while let idx = buffer.firstIndex(of: 0x0A) {
            let line = buffer.subdata(in: buffer.startIndex..<idx)
            buffer.removeSubrange(buffer.startIndex...idx)
            guard !line.isEmpty, let resp = JSONRPCResponse.parse(line), let id = resp.id else {
                continue // 通知或无法解析的行，忽略
            }
            lock.lock()
            let c = pending.removeValue(forKey: id)
            lock.unlock()
            c?.resume(returning: resp)
        }
    }

    private func handleEOF() {
        stdoutPipe?.fileHandleForReading.readabilityHandler = nil
        guard !stopping else { return }
        MCPLog.error("[MCP] \(config.name) stdout closed")
        failAllPending(MCPError(message: "MCP 进程输出流已关闭"))
        state = .error
    }

    private func failAllPending(_ error: Error) {
        lock.lock()
        let conts = pending
        pending.removeAll()
        lock.unlock()
        for (_, c) in conts {
            c.resume(throwing: error)
        }
    }
}
#endif
