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
            do {
                let context = ExecuteContext(budgetBytes: budgetBytes, deadlineSecs: deadline)
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
                    try await api.submitToolResult(callId: callID, resultJSON: nil, error: error)
                } else {
                    try await api.submitToolResult(callId: callID, resultJSON: Data("{}".utf8))
                }
                LogBuffer.shared.info("clientToolCall: \(toolName) result submitted")
            } catch {
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
