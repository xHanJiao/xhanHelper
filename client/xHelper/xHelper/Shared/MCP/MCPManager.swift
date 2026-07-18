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

    // 分页阅读：超大结果缓存（只留最近一条，翻页场景天然串行）。
    // 预算由服务端随 client_tool_call 事件下发（result_budget_bytes），
    // 无下发时 fallback 该默认值（与 service 端 clientToolInjectBudgetBytes 一致）。
    static let readMoreToolName = "result_read_more"
    static let fallbackResultBudgetBytes = 20000
    private var paged: PagedResult?

    private struct PagedResult {
        let token: String
        let serverName: String
        let pages: [String] // pre-computed pages (each a String, ready to JSON-encode)
    }

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

    // 供 /api/chat 请求体上报的 client_tools 数组（按各 server 的白名单过滤 + schema 瘦身）
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
                    item["parameters"] = slimSchema(schema, sc: sc)
                }
                out.append(item)
            }
            // 分页虚拟工具（客户端合成，不来自 MCP server）：超大结果的续读入口
            out.append([
                "server_name": sc.name,
                "name": Self.readMoreToolName,
                "description": "当上一个工具结果过大被分页（结果中 truncated=true）时，按页继续读取剩余内容。需提供 page_token（首页返回，绑定该次分页）和 cursor（页码）。",
                "parameters": [
                    "type": "object",
                    "properties": [
                        "cursor": ["type": "integer", "description": "页码，从 2 开始（第 1 页已随原结果返回）"],
                        "page_token": ["type": "string", "description": "分页令牌，与首页 truncated 结果中的 page_token 一致，防止读到已被覆盖的缓存"],
                    ],
                    "required": ["cursor", "page_token"],
                ],
            ])
        }
        return out
    }

    // 执行客户端工具。flatName 为服务端下发的扁平名 "serverName__toolName"。
    // budgetBytes: 服务端下发的注入预算（result_budget_bytes），用于决定分页阈值和分块大小。
    // deadlineSecs: 服务端下发的超时秒数（timeout_secs），客户端留 10% 余量；无下发时 fallback 默认。
    func callTool(flatName: String, argsJSON: Data, budgetBytes: Int = fallbackResultBudgetBytes, deadlineSecs: TimeInterval = 54) async throws -> Data {
        guard let sepRange = flatName.range(of: "__") else {
            throw MCPError(message: "非法工具名: \(flatName)")
        }
        let serverName = String(flatName[..<sepRange.lowerBound])
        let toolName = String(flatName[sepRange.upperBound...])

        // 分页虚拟工具：客户端直接处理，不经 MCP server（需在白名单校验前拦截）
        if toolName == Self.readMoreToolName {
            return readMoreChunk(serverName: serverName, argsJSON: argsJSON)
        }

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
        let startTime = CFAbsoluteTimeGetCurrent()
        var result = try await server.callTool(name: toolName, args: args, timeoutSecs: min(deadlineSecs, 55))

        // 自愈：结果命中特征串时，清理外部状态 → 重建 server → 重试一次。
        // 必须检查剩余时间预算：重建+重试耗时可能超服务端 timeout_secs。
        if needsRecovery(result, sc: sc) {
            let elapsed = CFAbsoluteTimeGetCurrent() - startTime
            let remaining = deadlineSecs - elapsed
            guard remaining >= 15 else {
                MCPLog.error("[MCP] \(serverName) recovery skipped: insufficient time (elapsed=\(Int(elapsed))s, deadline=\(Int(deadlineSecs))s)")
                throw MCPError(message: "工具执行异常（\(serverName) 状态异常且时间不足，无法自动恢复）。请稍后重试或手动重启浏览器。")
            }
            MCPLog.info("[MCP] \(serverName) stale state detected, cleanup + rebuild + retry (remaining=\(Int(remaining))s)")
            servers[serverName]?.stop()
            servers[serverName] = nil
            await runCleanup(sc)
            let fresh = try await ensureRunning(serverName)
            result = try await fresh.callTool(name: toolName, args: args, timeoutSecs: min(remaining, 55))
        }

        activity.markUsed(serverName)
        if sc.clearActivityTools.contains(toolName) {
            MCPLog.info("[MCP] \(serverName) activity cleared by tool: \(toolName)")
            activity.clear(serverName)
        }
        return paginateIfNeeded(slimResult(result, sc: sc), serverName: serverName, sc: sc, budgetBytes: budgetBytes)
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
        paged = nil

        guard closeExternal else { return }
        let used = activity.snapshot()
        for sc in config.servers where used.contains(sc.name) {
            await runCleanup(sc)
        }
    }

    // MARK: - 结果/Schema 瘦身

    // 递归剔除结果 JSON 中的噪音字段（stdout 重复/lifecycle 元数据等）
    private func slimResult(_ data: Data, sc: MCPServerConfig) -> Data {
        guard !sc.resultNoiseKeys.isEmpty,
              let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else {
            return data
        }
        let slimmed = Self.stripKeys(obj, keys: Set(sc.resultNoiseKeys))
        return (try? JSONSerialization.data(withJSONObject: slimmed)) ?? data
    }

    private static func stripKeys(_ value: Any, keys: Set<String>) -> Any {
        if let dict = value as? [String: Any] {
            var out: [String: Any] = [:]
            for (k, v) in dict where !keys.contains(k) {
                out[k] = stripKeys(v, keys: keys)
            }
            return out
        }
        if let arr = value as? [Any] {
            return arr.map { stripKeys($0, keys: keys) }
        }
        return value
    }

    // 超预算时缓存全文并返回第一页，后续通过 result_read_more(cursor, page_token) 续读。
    // budgetBytes = 服务端下发的注入预算；分页后每页序列化 JSON 不会超过该值。
    // sc.pageTextSource 决定如何从结果提取文本（.rawJSON 全文 / .mcpContentText 只取 content[].text）。
    private func paginateIfNeeded(_ data: Data, serverName: String, sc: MCPServerConfig, budgetBytes: Int) -> Data {
        guard data.count > budgetBytes else { return data }

        var text: String
        switch sc.pageTextSource {
        case .mcpContentText:
            if let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
               let content = obj["content"] as? [[String: Any]] {
                text = content.compactMap { $0["text"] as? String }.joined(separator: "\n")
            } else {
                text = String(decoding: data, as: UTF8.self)
            }
        case .rawJSON:
            text = String(decoding: data, as: UTF8.self)
        }

        guard !text.isEmpty else { return data }

        let token = Self.randomPageToken()
        let pages = Self.chunkPages(text: text, budgetBytes: budgetBytes)
        let total = pages.count

        let firstObj: [String: Any] = [
            "truncated": true,
            "total_bytes": text.utf8.count,
            "chunk": "1/\(total)",
            "page_token": token,
            "note": "结果过大已分页（每页≤\(budgetBytes)字节）\(total>1 ? "续读调 \(Self.readMoreToolName)(cursor=2, page_token=\"\(token)\")" : "")。通常建议改用 snapshot/get_text 等定向工具获取目标内容，而非通读全文。",
            "content": pages[0],
        ]
        paged = PagedResult(token: token, serverName: serverName, pages: pages)
        MCPLog.info("[MCP] result paginated: \(data.count) bytes → \(total) pages (budget=\(budgetBytes))")
        return (try? JSONSerialization.data(withJSONObject: firstObj)) ?? Data(#"{"truncated":true}"#.utf8)
    }

    // 将长文本切成若干页，每页序列化为 JSON 对象（含 content 字段）时不超 budgetBytes。
    // 默认分页信封（不含 content）约 400-600 字节，分配 80% 预算给内容。
    private static func chunkPages(text: String, budgetBytes: Int) -> [String] {
        let contentBudget = max(1, budgetBytes * 80 / 100)
        var pages: [String] = []
        var remaining = text[...]
        var pageNum = 0

        while !remaining.isEmpty {
            pageNum += 1
            if pageNum > 300 {
                // 极端保护：超过 300 页裁尾
                MCPLog.error("[MCP] pagination capped at 300 pages, remaining text dropped")
                break
            }
            let chunk = Self.extractUTF8Chunk(from: remaining, maxBytes: contentBudget)
            pages.append(String(chunk))
            let consumed = chunk.utf8.count
            if consumed >= remaining.utf8.count {
                break
            }
            // 移动到 consumed 之后的字符（Character 边界安全）
            let idx = remaining.utf8.index(remaining.utf8.startIndex, offsetBy: consumed)
            remaining = remaining.suffix(from: idx)
        }
        return pages
    }

    // 从字符串头部切出 ≤maxBytes 个 UTF-8 字节，保证不切在多字节字符中间。
    private static func extractUTF8Chunk(from s: Substring, maxBytes: Int) -> Substring {
        let utf8 = s.utf8
        if utf8.count <= maxBytes { return s }
        var byteCount = 0
        var endIdx = s.startIndex
        for ch in s {
            let next = byteCount + ch.utf8.count
            if next > maxBytes { break }
            byteCount = next
            endIdx = s.index(after: endIdx)
        }
        return endIdx == s.startIndex ? s.prefix(0) : s[..<endIdx]
    }

    // result_read_more 处理：按 cursor 返回缓存页
    private func readMoreChunk(serverName: String, argsJSON: Data) -> Data {
        let args = (try? JSONSerialization.jsonObject(with: argsJSON)) as? [String: Any] ?? [:]

        guard let pageToken = args["page_token"] as? String, !pageToken.isEmpty else {
            return jsonData(["error": "缺少必填参数 page_token"])
        }
        guard let cursorNum = args["cursor"] as? NSNumber else {
            return jsonData(["error": "缺少或非法参数 cursor（需为整数）"])
        }
        let cursor = cursorNum.intValue

        guard let p = paged, p.token == pageToken, p.serverName == serverName else {
            return jsonData(["error": "分页缓存已失效（token 不匹配或被新工具结果覆盖），请重新执行原工具"])
        }
        let total = p.pages.count
        guard cursor >= 1, cursor <= total else {
            return jsonData(["error": "cursor 越界：有效范围 1-\(total)"])
        }
        return jsonData([
            "chunk": "\(cursor)/\(total)",
            "has_more": cursor < total,
            "content": p.pages[cursor - 1],
        ])
    }

    private func jsonData(_ obj: [String: Any]) -> Data {
        (try? JSONSerialization.data(withJSONObject: obj)) ?? Data("{}".utf8)
    }

    private static func randomPageToken() -> String {
        String(UInt32.random(in: 0..<UInt32.max), radix: 36)
    }

    // 从工具 schema 中剔除 LLM 用不到的公共高级参数
    private func slimSchema(_ schema: [String: Any], sc: MCPServerConfig) -> [String: Any] {
        guard !sc.schemaNoiseParams.isEmpty,
              var props = schema["properties"] as? [String: Any] else {
            return schema
        }
        var out = schema
        for k in sc.schemaNoiseParams {
            props.removeValue(forKey: k)
        }
        out["properties"] = props
        if let required = schema["required"] as? [String] {
            out["required"] = required.filter { !sc.schemaNoiseParams.contains($0) }
        }
        return out
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
