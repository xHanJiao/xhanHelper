#if os(macOS)
@preconcurrency import Foundation

// 单条结果分页时文本提取策略：不同 MCP server 结果格式不同，
// 页内容取法由 server 维度配置，不在通用路径假设结构。
enum PageTextSource: String, Sendable {
    case rawJSON          // 默认：不假设结构，直接以原始 JSON 字符串分页
    case mcpContentText   // 取 content[].text 拼接为分页文本（如 agent-browser）
}

// 单个 MCP server 的配置
struct MCPServerConfig: Sendable {
    let name: String              // server_name，如 "browser"（禁止含 "__"）
    let command: String           // 可执行文件名或绝对路径，如 "agent-browser"
    let args: [String]            // 如 ["mcp"]
    let env: [String: String]?
    let idleTimeoutSecs: TimeInterval // 空闲回收阈值，默认 300
    let description: String
    let toolAllowlist: Set<String>?   // 上报/执行的工具白名单，nil = 不过滤
    let cleanupCommand: [String]?     // 退出/自愈时的清理命令（一次性子进程），nil = 无需清理
    let externalResourceName: String? // 对用户展示的外部资源名（退出确认弹窗用），nil = 不参与弹窗
    let recoverPatterns: [String]     // 工具结果含任一特征串时触发清理+重建+重试（自愈）
    let resultNoiseKeys: [String]     // 回传前从工具结果 JSON 中递归剔除的噪音字段（省 token）
    let schemaNoiseParams: [String]   // 上报前从工具 schema properties 中剔除的高级参数（省 token）
    let pageTextSource: PageTextSource // 分页时如何提取文本内容（.rawJSON=原始JSON, .mcpContentText=content[].text拼接）
    let clearActivityTools: Set<String> // 执行成功后清除 activity 的工具名（如 agent_browser_close）
}

// MCPManager 的配置
struct MCPManagerConfig: Sendable {
    let maxConcurrent: Int        // 同时运行的 MCP server 上限
    let servers: [MCPServerConfig]

    nonisolated static let `default` = MCPManagerConfig(
        maxConcurrent: 2,
        servers: []
    )
}
#endif
