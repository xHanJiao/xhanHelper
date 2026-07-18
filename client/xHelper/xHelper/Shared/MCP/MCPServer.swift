#if os(macOS)
@preconcurrency import Foundation

enum MCPServerState: Sendable, Equatable {
    case stopped
    case starting
    case ready
    case error

    nonisolated static func == (lhs: Self, rhs: Self) -> Bool {
        switch (lhs, rhs) {
        case (.stopped, .stopped), (.starting, .starting), (.ready, .ready), (.error, .error):
            return true
        default: return false
        }
    }
}

// MCPServer — 单个 MCP 子进程封装。
// JSON-RPC 2.0 over stdio（newline-delimited），pending 路由用
// CheckedContinuation（等价 Go 的 pending map + channel）。
// 生命周期由 MCPManager（actor）串行驱动；readLoop 回调线程只经 lock 触碰 pending。
final class MCPServer: @unchecked Sendable {
    let config: MCPServerConfig
    private(set) nonisolated(unsafe) var state: MCPServerState = .stopped
    private(set) nonisolated(unsafe) var toolsCache: [MCPToolDef] = []
    private(set) nonisolated(unsafe) var lastUsedAt = Date()
    private(set) nonisolated(unsafe) var inFlight = 0

    private nonisolated(unsafe) var process: Process?
    private nonisolated(unsafe) var stdinPipe: Pipe?
    private nonisolated(unsafe) var stdoutPipe: Pipe?
    private nonisolated(unsafe) var buffer = Data()
    private nonisolated(unsafe) var nextID: Int64 = 0
    private nonisolated(unsafe) var pending: [Int64: CheckedContinuation<JSONRPCResponse, Error>] = [:]
    private let lock = NSLock()
    private nonisolated(unsafe) var stopping = false

    nonisolated init(config: MCPServerConfig) {
        self.config = config
    }

    // MARK: - 启动 + MCP 握手

    nonisolated func start() async throws {
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

    nonisolated func stop() {
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
    nonisolated func callTool(name: String, args: [String: Any], timeoutSecs: TimeInterval = 55) async throws -> Data {
        lock.withLock { inFlight += 1 }
        defer {
            lock.withLock {
                inFlight -= 1
                lastUsedAt = Date()
            }
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

    nonisolated private func call(method: String, params: [String: Any]? = nil, timeoutSecs: TimeInterval) async throws -> JSONRPCResponse {
        let id = lock.withLock {
            nextID += 1
            return nextID
        }

        return try await withCheckedThrowingContinuation { cont in
            lock.withLock { pending[id] = cont }

            do {
                try send(JSONRPC.requestData(id: id, method: method, params: params))
            } catch {
                let c = lock.withLock { pending.removeValue(forKey: id) }
                c?.resume(throwing: error)
                return
            }

            // 超时兜底
            DispatchQueue.global().asyncAfter(deadline: .now() + timeoutSecs) { [weak self] in
                guard let self else { return }
                let c = self.lock.withLock { self.pending.removeValue(forKey: id) }
                c?.resume(throwing: MCPError(message: "\(method) 超时（\(Int(timeoutSecs))s）"))
            }
        }
    }

    nonisolated private func send(_ data: Data) throws {
        guard let fh = stdinPipe?.fileHandleForWriting else {
            throw MCPError(message: "MCP 进程未运行")
        }
        try fh.write(contentsOf: data)
    }

    // MARK: - readLoop

    nonisolated private func handleData(_ data: Data) {
        buffer.append(data)
        while let idx = buffer.firstIndex(of: 0x0A) {
            let line = buffer.subdata(in: buffer.startIndex..<idx)
            buffer.removeSubrange(buffer.startIndex...idx)
            guard !line.isEmpty, let resp = JSONRPCResponse.parse(line), let id = resp.id else {
                continue // 通知或无法解析的行，忽略
            }
            let c = lock.withLock { pending.removeValue(forKey: id) }
            c?.resume(returning: resp)
        }
    }

    nonisolated private func handleEOF() {
        stdoutPipe?.fileHandleForReading.readabilityHandler = nil
        guard !stopping else { return }
        MCPLog.error("[MCP] \(config.name) stdout closed")
        failAllPending(MCPError(message: "MCP 进程输出流已关闭"))
        state = .error
    }

    nonisolated private func failAllPending(_ error: Error) {
        let conts = lock.withLock {
            let r = pending
            pending.removeAll()
            return r
        }
        for (_, c) in conts {
            c.resume(throwing: error)
        }
    }
}
#endif
