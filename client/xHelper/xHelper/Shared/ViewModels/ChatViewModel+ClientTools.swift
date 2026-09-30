import Foundation

// 客户端工具（本地 MCP）对接：
// - 发送消息时上报 client_tools（工具 schema）
// - 处理 client_tool_call 事件：本地执行 → POST /api/chat/tool-result 回传
// MCP 进程只在 macOS 存在（Process 在 iOS 不可用），iOS 侧为空实现。
extension ChatViewModel {

    // 收集本地工具 schema，供 /api/chat 请求体 client_tools 字段
    static func collectClientTools() async -> [[String: Any]]? {
        #if os(macOS)
        let tools = await LocalToolRegistry.shared.clientToolsPayload()
        return tools.isEmpty ? nil : tools
        #else
        return nil
        #endif
    }

    // 处理服务端下发的 client_tool_call 事件。
    // data: {"call_id":"...","tool_name":"serverName__toolName","arguments":{...}}
    func handleClientToolCall(_ data: String) {
        #if os(macOS)
        guard let d = data.data(using: .utf8),
              let obj = (try? JSONSerialization.jsonObject(with: d)) as? [String: Any],
              let callID = obj["call_id"] as? String,
              let toolName = obj["tool_name"] as? String else {
            LogBuffer.shared.error("clientToolCall: bad payload: \(data.prefix(200))")
            return
        }
        let args = (obj["arguments"] as? [String: Any]) ?? [:]
        let budgetBytes = (obj["result_budget_bytes"] as? NSNumber)?.intValue
        let timeoutSecs = (obj["timeout_secs"] as? NSNumber)?.doubleValue
        let deadline = (timeoutSecs != nil) ? (timeoutSecs! * 0.9) : nil
        let owner = obj["owner"] as? String
        // 服务端下发 payload 补的会话上下文（附表 A：客户端执行视角靠它们归组/挂父）。
        let conversationID = obj["conversation_id"] as? String
        let turnID = obj["turn_id"] as? String
        let bridgeEventID = obj["bridge_event_id"] as? String
        let api = apiClient

        print("clientToolCall: \(toolName) call_id=\(callID) owner=\(owner ?? "nil") conversation_id=\(conversationID ?? "nil") turn_id=\(turnID ?? "nil")")
        Task {
            // 客户端执行视角区间：started/ended 用共享 pair_id 配对，父 = 服务端那条 tool.bridge.dispatched。
            let execPair = UUID().uuidString
            var execOutcome = "completed"
            var execErrorType: String?
            ActivityClient.emit(ActivityEvent(
                type: "client.tool.exec.started", actor: "assistant", trigger: "manual",
                bizKey: callID, parentEventId: bridgeEventID, pairId: execPair,
                conversationId: conversationID, turnId: turnID, callId: callID,
                attrs: ["tool_name": .string(toolName)]))
            defer {
                ActivityClient.emit(ActivityEvent(
                    type: "client.tool.exec.ended", actor: "assistant", trigger: "manual",
                    bizKey: callID, outcome: execOutcome, errorType: execErrorType,
                    parentEventId: bridgeEventID, pairId: execPair,
                    conversationId: conversationID, turnId: turnID, callId: callID,
                    attrs: ["tool_name": .string(toolName)]))
            }
            let isBrowser = toolName.hasPrefix("browser__")
            let isFS = toolName.hasPrefix("fs__")
            do {
                // 子任务用户介入（ask_user）：弹 AskUserBar + 系统通知，用户可输入回答或直接已处理/跳过
                if toolName == "ui__ask_user" {
                    let message = (args["message"] as? String) ?? "子任务需要你的帮助"
                    LogBuffer.shared.info("askUser: \(message.prefix(120))")
                    let reply = await self.awaitUserRequest(callID: callID, message: message, timeoutSecs: timeoutSecs, owner: owner)
                    // 手递手收尾：无论完成/跳过/超时，交还标签给 agent 工作区
                    if let owner { BrowserToolProvider.shared.resolveHandoff(owner: owner) }
                    if let reply {
                        var payload: [String: Any] = ["answered": true]
                        if !reply.isEmpty { payload["reply"] = reply }
                        let data = try JSONSerialization.data(withJSONObject: payload)
                        try await api.submitToolResult(callId: callID, resultJSON: data)
                    } else {
                        try await api.submitToolResult(callId: callID, resultJSON: nil, error: "用户未处理，已跳过")
                    }
                    return
                }

                if isFS {
                    if let blocked = LocalToolRegistry.shared.fsBlockedPaths(arguments: args) {
                        LogBuffer.shared.warning("clientToolCall: \(toolName) blocked: \(blocked)")
                        execOutcome = "failed"; execErrorType = "blocked"
                        try await api.submitToolResult(callId: callID, resultJSON: nil, error: blocked)
                        return
                    }
                }

                // shell / shell_peek：未命中白名单 / workdir 未授权 / 语法不支持 → 直接拒绝（不弹确认）
                let shellScope = ShellToolProvider.scope(forFlatName: toolName)
                let isShell = shellScope != nil
                if isShell {
                    if let blocked = LocalToolRegistry.shared.shellBlockedReason(flatName: toolName, arguments: args) {
                        LogBuffer.shared.warning("clientToolCall: \(toolName) blocked: \(blocked)")
                        execOutcome = "failed"; execErrorType = "blocked"
                        try await api.submitToolResult(callId: callID, resultJSON: nil, error: blocked)
                        return
                    }
                }

                // 工具分级：L3（高风险写）执行前需用户确认（60s 超时视为拒绝）
                // S2/G6：交互工具在高敏域或 http 明文页上动态升 L3（防注入诱导自动操作账户）。
                // forcedL3 标记"敏感上下文升的级"，任何信任档都不跳过（防注入最后一道）。
                var level = LocalToolRegistry.shared.riskLevel(for: toolName, arguments: args)
                var forcedL3 = false
                if level < 3, isBrowser, BrowserToolProvider.shared.shouldElevateToL3(flatName: toolName, arguments: args) {
                    level = 3
                    forcedL3 = true
                    LogBuffer.shared.warning("clientToolCall: \(toolName) elevated to L3 (sensitive/plaintext context)")
                }

                // 用户标签写入授权：agent 写工具命中用户自己的标签且该任务未授权 → 升 L3 弹确认，
                // 允许后记入该任务（owner）的授权集合（任务级缓存），后续免重复确认。
                let needsUserTabGrant = isBrowser
                    && BrowserToolProvider.shared.needsUserTabGrant(tool: toolName, arguments: args, owner: owner)
                if needsUserTabGrant {
                    level = 3
                    forcedL3 = true
                    LogBuffer.shared.warning("clientToolCall: \(toolName) needs user-tab grant (owner=\(owner ?? "nil"))")
                }

                // S6：跨域下载在确认信息里标注来源，防下载来源伪装
                var confirmArgs = args
                if needsUserTabGrant {
                    confirmArgs["__user_tab_grant"] = true
                }
                if toolName == "browser__agent_browser_download",
                   let urlStr = args["url"] as? String, let url = URL(string: urlStr),
                   let dlHost = url.host, dlHost != BrowserToolProvider.shared.activeURL?.host {
                    confirmArgs["跨域下载"] = dlHost
                }

                if level >= 3 {
                    // shell L4：命令级高危，红色确认条 + 信任档不跳过（完全信任可跳过）
                    let shellHardened = shellScope == .full && level >= 4
                    // shell：生成人话解释（用户不读原始命令，只看这句判断意图）
                    var shellExplain: String?
                    if shellScope == .full, let cmd = args["command"] as? String {
                        let wd = ShellToolProvider.resolveWorkdir(args["workdir"] as? String)
                        let stages = ShellCommandParser.evaluate(command: cmd, workdir: wd).stages
                        if !stages.isEmpty { shellExplain = ShellCommandDescriber.describe(stages: stages) }
                    }
                    // fs 写/编辑工具：确认前预演 diff（不落盘），确认条展示。大文件全量读放后台执行。
                    var diffText: String?
                    var additions: Int?
                    var deletions: Int?
                    var fsPath: String?
                    if isFS, toolName == "fs__write" || toolName == "fs__edit" {
                        let pathArg = args["path"] as? String
                        let contentArg = args["content"] as? String
                        let modeArg = args["mode"] as? String
                        let oldArg = args["old_string"] as? String
                        let newArg = args["new_string"] as? String
                        let replaceAllArg = args["replace_all"] as? Bool
                        let preview = await Task.detached(priority: .userInitiated) {
                            FileSystemToolProvider.previewForTool(tool: toolName, arguments: [
                                "path": pathArg ?? "",
                                "content": contentArg ?? "",
                                "mode": modeArg ?? "overwrite",
                                "old_string": oldArg ?? "",
                                "new_string": newArg ?? "",
                                "replace_all": replaceAllArg ?? false,
                            ])
                        }.value
                        if let preview {
                            diffText = preview.diffText
                            additions = preview.additions
                            deletions = preview.deletions
                            fsPath = preview.filePath
                        }
                    }
                    let confirmed = await self.awaitToolConfirmation(
                        toolName: toolName,
                        arguments: confirmArgs,
                        elevated: forcedL3,
                        hardened: shellHardened,
                        browserArgsSummary: isBrowser,
                        diffText: diffText,
                        additions: additions,
                        deletions: deletions,
                        filePath: fsPath,
                        explanation: shellExplain
                    )
                    guard confirmed else {
                        LogBuffer.shared.warning("clientToolCall: \(toolName) rejected by user")
                        execOutcome = "cancelled"; execErrorType = "rejected"
                        try await api.submitToolResult(callId: callID, resultJSON: nil, error: "用户拒绝执行")
                        return
                    }
                    // 用户标签授权允许 → 记入该任务（owner）授权集合
                    if needsUserTabGrant, let owner {
                        BrowserToolProvider.shared.grantUserTabOps(owner: owner)
                        LogBuffer.shared.info("clientToolCall: user-tab ops granted for owner=\(owner)")
                    }
                }

                if isFS, level == 3 {
                    let paths = FileSystemToolProvider.extractPaths(flatName: toolName, arguments: args)
                    for path in paths {
                        if !DirectoryAuthorizationManager.shared.isAuthorized(path) {
                            DirectoryAuthorizationManager.shared.authorize(path, permanent: false)
                        }
                    }
                }

                let context = ExecuteContext(
                    budgetBytes: budgetBytes, deadlineSecs: deadline, owner: owner,
                    conversationId: conversationID, turnId: turnID, bridgeEventId: bridgeEventID)
                let result = try await LocalToolRegistry.shared.execute(
                    flatName: toolName,
                    arguments: args,
                    context: context
                )

                if let action = result.clientAction {
                    handleClientAction(action)
                }

                if let data = result.serverResult {
                    try await api.submitToolResult(callId: callID, resultJSON: data)
                } else if let error = result.serverError {
                    execOutcome = "failed"; execErrorType = "tool_error"
                    try await api.submitToolResult(callId: callID, resultJSON: nil, error: error)
                } else {
                    try await api.submitToolResult(callId: callID, resultJSON: Data("{}".utf8))
                }
                LogBuffer.shared.info("clientToolCall: \(toolName) result submitted")
            } catch {
                execOutcome = "failed"; execErrorType = "exception"
                LogBuffer.shared.error("clientToolCall: \(toolName) failed: \(error.localizedDescription)")
                do {
                    try await api.submitToolResult(callId: callID, resultJSON: nil, error: error.localizedDescription)
                } catch {
                    LogBuffer.shared.error("clientToolCall: submit error result failed: \(error.localizedDescription)")
                }
            }
        }
        #else
        LogBuffer.shared.warning("clientToolCall ignored on iOS")
        #endif
    }

    private func handleClientAction(_ action: ClientAction) {
        switch action.type {
        case "chart":
            guard let cd = try? JSONDecoder().decode(ChartData.self, from: action.payload) else {
                LogBuffer.shared.error("clientAction chart: decode failed")
                return
            }
            insertLocalArtifact(.chart(cd))
        default:
            LogBuffer.shared.warning("clientAction: unknown type \(action.type)")
        }
    }
}
