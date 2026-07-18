#if os(macOS)
import Foundation

// MCPManager — 多 MCP server 生命周期管理（actor 保证串行）。
// 懒启动 / 空闲回收 / LRU 抢占（maxConcurrent），工具 schema 缓存供 client_tools 上报。
actor MCPManager {
    static let shared = MCPManager(config: .default)

    private let config: MCPManagerConfig
    private var servers: [String: MCPServer] = [:]
    private var toolCaches: [String: [MCPToolDef]] = [:] // server 停止后仍保留 schema
    private var idleReaperTask: Task<Void, Never>?

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
        return try await server.callTool(name: toolName, args: args)
    }

    func shutdown() {
        idleReaperTask?.cancel()
        idleReaperTask = nil
        for (_, server) in servers {
            server.stop()
        }
        servers.removeAll()
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
