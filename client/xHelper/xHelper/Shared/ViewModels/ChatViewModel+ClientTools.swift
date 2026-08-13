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
        let api = apiClient

        LogBuffer.shared.info("clientToolCall: \(toolName) call_id=\(callID)")
        Task {
            let isBrowser = toolName.hasPrefix("browser__")
            let isFS = toolName.hasPrefix("fs__")
            do {
                // 子任务用户介入（ask_user）：弹 AskUserBar + 系统通知，等用户 ok/deny
                if toolName == "ui__ask_user" {
                    let message = (args["message"] as? String) ?? "子任务需要你的帮助"
                    LogBuffer.shared.info("askUser: \(message.prefix(120))")
                    let answered = await self.awaitUserRequest(callID: callID, message: message, timeoutSecs: timeoutSecs)
                    if answered {
                        try await api.submitToolResult(callId: callID, resultJSON: Data(#"{"answered":true}"#.utf8))
                    } else {
                        try await api.submitToolResult(callId: callID, resultJSON: nil, error: "用户未处理，已跳过")
                    }
                    return
                }

                if isFS {
                    if let blocked = LocalToolRegistry.shared.fsBlockedPaths(arguments: args) {
                        LogBuffer.shared.warning("clientToolCall: \(toolName) blocked: \(blocked)")
                        try await api.submitToolResult(callId: callID, resultJSON: nil, error: blocked)
                        return
                    }
                }

                // 工具分级：L3（高风险写）执行前需用户确认（60s 超时视为拒绝）
                // S2/G6：交互工具在高敏域或 http 明文页上动态升 L3（防注入诱导自动操作账户）。
                // forcedL3 标记"敏感上下文升的级"，信任模式也不跳过。
                var level = LocalToolRegistry.shared.riskLevel(for: toolName, arguments: args)
                var forcedL3 = false
                if level < 3, isBrowser, BrowserToolProvider.shared.shouldElevateToL3(flatName: toolName) {
                    level = 3
                    forcedL3 = true
                    LogBuffer.shared.warning("clientToolCall: \(toolName) elevated to L3 (sensitive/plaintext context)")
                }

                // S6：跨域下载在确认信息里标注来源，防下载来源伪装
                var confirmArgs = args
                if toolName == "browser__agent_browser_download",
                   let urlStr = args["url"] as? String, let url = URL(string: urlStr),
                   let dlHost = url.host, dlHost != BrowserToolProvider.shared.activeURL?.host {
                    confirmArgs["跨域下载"] = dlHost
                }

                if level >= 3 {
                    let confirmed = await self.awaitToolConfirmation(toolName: toolName, arguments: confirmArgs, force: forcedL3, bridgeToBrowser: isBrowser)
                    guard confirmed else {
                        LogBuffer.shared.warning("clientToolCall: \(toolName) rejected by user")
                        try await api.submitToolResult(callId: callID, resultJSON: nil, error: "用户拒绝执行")
                        return
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

                // G2：浏览器工具执行可见性（执行前广播"运行中"，完成/失败消退）
                if isBrowser { BrowserActivityCenter.shared.start(flatName: toolName, arguments: args) }

                let context = ExecuteContext(budgetBytes: budgetBytes, deadlineSecs: deadline)
                let result = try await LocalToolRegistry.shared.execute(
                    flatName: toolName,
                    arguments: args,
                    context: context
                )

                if isBrowser { BrowserActivityCenter.shared.finish() }

                if let action = result.clientAction {
                    handleClientAction(action)
                }

                if let data = result.serverResult {
                    try await api.submitToolResult(callId: callID, resultJSON: data)
                } else if let error = result.serverError {
                    try await api.submitToolResult(callId: callID, resultJSON: nil, error: error)
                } else {
                    try await api.submitToolResult(callId: callID, resultJSON: Data("{}".utf8))
                }
                LogBuffer.shared.info("clientToolCall: \(toolName) result submitted")
            } catch {
                if isBrowser { BrowserActivityCenter.shared.clear() }
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
            insertChartMessage(cd)
        default:
            LogBuffer.shared.warning("clientAction: unknown type \(action.type)")
        }
    }
}
