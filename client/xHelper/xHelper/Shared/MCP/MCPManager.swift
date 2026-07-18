#if os(macOS)
import Foundation

// MCPActivityTracker — 记录本次 App 会话内使用过的 server（锁保护）。
// 供退出确认弹窗在同步上下文（applicationShouldTerminate）读取。
final class MCPActivityTracker: @unchecked Sendable {
    private let lock = NSLock()
    private var used: Set<String> = []

    func markUsed(_ name: String) {
        lock.lock(); defer { lock.unlock() }
        used.insert(name)
    }

    func clear(_ name: String) {
        lock.lock(); defer { lock.unlock() }
        used.remove(name)
    }

    func snapshot() -> Set<String> {
        lock.lock(); defer { lock.unlock() }
        return used
    }
}

// MCPManager — 多 MCP server 生命周期管理（actor 保证串行）。
// 懒启动 / 空闲回收 / LRU 抢占（maxConcurrent），工具 schema 缓存供 client_tools 上报。
actor MCPManager {
    static let shared = MCPManager(config: .default)

    private let config: MCPManagerConfig
    private var servers: [String: MCPServer] = [:]
    private var toolCaches: [String: [MCPToolDef]] = [:] // server 停止后仍保留 schema
    private var idleReaperTask: Task<Void, Never>?
    nonisolated let activity = MCPActivityTracker()

    init(config: MCPManagerConfig) {
        self.config = config
    }

    // MARK: - 对外接口

    // 预热：启动所有配置的 server 拉一次 tools/list（缓存 schema），失败只记日志。
    // App 启动时后台调用；server 之后由空闲回收自然停掉，schema 缓存保留。
    func refreshToolCache() async {
        for sc in config.servers {
            do {
                let server = try await ensureRunning(sc.name)
                toolCaches[sc.name] = server.toolsCache
            } catch {
                MCPLog.error("[MCP] refreshToolCache \(sc.name): \(error.localizedDescription)")
            }
        }
        startIdleReaperIfNeeded()
    }

    // 供 /api/chat 请求体上报的 client_tools 数组（按各 server 的白名单过滤）
    func clientToolsPayload() -> [[String: Any]] {
        var out: [[String: Any]] = []
        for sc in config.servers {
            guard let tools = toolCaches[sc.name] else { continue }
            for t in tools {
                if let allowlist = sc.toolAllowlist, !allowlist.contains(t.name) { continue }
                var item: [String: Any] = [
                    "server_name": sc.name,
                    "name": t.name,
                    "description": t.description,
                ]
                if let schema = t.inputSchema {
                    item["parameters"] = schema
                }
                out.append(item)
            }
        }
        return out
    }

    // 执行客户端工具。flatName 为服务端下发的扁平名 "serverName__toolName"。
    func callTool(flatName: String, argsJSON: Data) async throws -> Data {
        guard let sepRange = flatName.range(of: "__") else {
            throw MCPError(message: "非法工具名: \(flatName)")
        }
        let serverName = String(flatName[..<sepRange.lowerBound])
        let toolName = String(flatName[sepRange.upperBound...])

        // 执行侧白名单校验：服务端按 server_name 前缀转发，
        // LLM 幻觉调用未上报的工具（如 eval）也会被转发过来，必须在这里拒绝
        guard let sc = config.servers.first(where: { $0.name == serverName }) else {
            throw MCPError(message: "未配置的 MCP server: \(serverName)")
        }
        if let allowlist = sc.toolAllowlist, !allowlist.contains(toolName) {
            MCPLog.error("[MCP] tool not in allowlist, rejected: \(flatName)")
            throw MCPError(message: "工具 \(toolName) 不在允许列表中，禁止调用。请使用已提供的工具完成任务。")
        }

        let args = (try? JSONSerialization.jsonObject(with: argsJSON)) as? [String: Any] ?? [:]
        let server = try await ensureRunning(serverName)
        var result = try await server.callTool(name: toolName, args: args)

        // 自愈：结果命中特征串（如坏 daemon 导致的 Auto-launch failed）时，
        // 清理外部状态 → 重建 server → 重试一次。覆盖上次 App 崩溃/强退留下的残留。
        if needsRecovery(result, sc: sc) {
            MCPLog.error("[MCP] \(serverName) stale state detected, cleanup + rebuild + retry once")
            servers[serverName]?.stop()
            servers[serverName] = nil
            await runCleanup(sc)
            let fresh = try await ensureRunning(serverName)
            result = try await fresh.callTool(name: toolName, args: args)
        }

        activity.markUsed(serverName)
        return result
    }

    // 弹窗清单：本次会话用过、且声明了外部资源名的 server
    nonisolated func activeExternalResources() -> [String] {
        let used = activity.snapshot()
        return config.servers.compactMap { sc in
            guard used.contains(sc.name), let res = sc.externalResourceName else { return nil }
            return res
        }
    }

    // 停止所有 MCP 进程；closeExternal=true 时对本次会话用过的 server 追加执行清理命令
    // （关掉 agent-browser daemon 拉起的 Chrome 等外部资源）。
    func shutdown(closeExternal: Bool) async {
        idleReaperTask?.cancel()
        idleReaperTask = nil
        for (_, server) in servers {
            server.stop()
        }
        servers.removeAll()

        guard closeExternal else { return }
        let used = activity.snapshot()
        for sc in config.servers where used.contains(sc.name) {
            await runCleanup(sc)
        }
    }

    // MARK: - 自愈/清理

    private func needsRecovery(_ result: Data, sc: MCPServerConfig) -> Bool {
        guard sc.cleanupCommand != nil, !sc.recoverPatterns.isEmpty,
              let text = String(data: result, encoding: .utf8) else { return false }
        return sc.recoverPatterns.contains { text.contains($0) }
    }

    // 一次性子进程执行清理命令，最多等 5s
    private func runCleanup(_ sc: MCPServerConfig) async {
        guard let cmd = sc.cleanupCommand, !cmd.isEmpty else { return }
        let p = Process()
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:" + (env["PATH"] ?? "/usr/bin:/bin")
        p.environment = env
        p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        p.arguments = cmd
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice

        var launched = false
        await withCheckedContinuation { (cont: CheckedContinuation<Void, Never>) in
            // handler 必须在 run() 前设置，避免命令秒退时错过回调
            p.terminationHandler = { _ in cont.resume() }
            do {
                try p.run()
                launched = true
                // 看门狗：5s 未退出则终止（terminate 触发 terminationHandler）
                DispatchQueue.global().asyncAfter(deadline: .now() + 5) {
                    if p.isRunning { p.terminate() }
                }
            } catch {
                MCPLog.error("[MCP] cleanup launch failed: \(error.localizedDescription)")
                cont.resume() // run 失败时 handler 不会触发，手动结束等待
            }
        }
        guard launched else { return }
        MCPLog.info("[MCP] cleanup executed: \(cmd.joined(separator: " "))")
        activity.clear(sc.name)
    }

    // MARK: - 生命周期

    func ensureRunning(_ name: String) async throws -> MCPServer {
        guard let sc = config.servers.first(where: { $0.name == name }) else {
            throw MCPError(message: "未配置的 MCP server: \(name)")
        }

        if let existing = servers[name] {
            switch existing.state {
            case .ready:
                return existing
            case .starting:
                throw MCPError(message: "\(name) 正在启动，请稍后重试")
            case .stopped, .error:
                existing.stop()
                servers[name] = nil
            }
        }

        // 并发上限：LRU 抢占最老的空闲 server
        while servers.count >= config.maxConcurrent {
            guard let victim = findLRUVictim() else {
                throw MCPError(message: "MCP server 并发已满且无可回收实例")
            }
            MCPLog.info("[MCP] LRU evict \(victim.config.name)")
            victim.stop()
            servers[victim.config.name] = nil
        }

        let server = MCPServer(config: sc)
        servers[name] = server
        do {
            try await server.start()
        } catch {
            servers[name] = nil
            throw error
        }
        toolCaches[name] = server.toolsCache
        startIdleReaperIfNeeded()
        return server
    }

    private func findLRUVictim() -> MCPServer? {
        var oldest: MCPServer?
        for (_, s) in servers {
            if s.inFlight > 0 { continue }
            if oldest == nil || s.lastUsedAt < oldest!.lastUsedAt {
                oldest = s
            }
        }
        return oldest
    }

    // MARK: - 空闲回收

    private func startIdleReaperIfNeeded() {
        guard idleReaperTask == nil else { return }
        idleReaperTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 30_000_000_000)
                await self?.reapIdle()
            }
        }
    }

    private func reapIdle() {
        for (name, s) in servers {
            guard s.state == .ready, s.inFlight == 0 else { continue }
            if Date().timeIntervalSince(s.lastUsedAt) > s.config.idleTimeoutSecs {
                MCPLog.info("[MCP] idle reap \(name)")
                s.stop()
                servers[name] = nil
            }
        }
    }
}
#endif
