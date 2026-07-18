#if os(macOS)
import Foundation

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
}

// MCPManager 的配置
struct MCPManagerConfig: Sendable {
    let maxConcurrent: Int        // 同时运行的 MCP server 上限
    let servers: [MCPServerConfig]

    static let `default` = MCPManagerConfig(
        maxConcurrent: 2,
        servers: [
            MCPServerConfig(
                name: "browser",
                command: "agent-browser",
                args: ["mcp"],
                env: [
                    "AGENT_BROWSER_HEADED": "true", // 显示浏览器窗口（默认 headless）
                    "AGENT_BROWSER_ARGS": "--disable-blink-features=AutomationControlled", // 降低自动化特征，缓解反爬
                ],
                idleTimeoutSecs: 300,
                description: "本地浏览器自动化（agent-browser）",
                // core profile 29 个工具中裁剪 tab/eval/close 等，控制 prompt token 开销
                toolAllowlist: [
                    // 导航
                    "agent_browser_open",
                    "agent_browser_close",
                    // 读取
                    "agent_browser_read",
                    "agent_browser_snapshot",
                    "agent_browser_get_text",
                    "agent_browser_get_url",
                    "agent_browser_get_title",
                    "agent_browser_screenshot",
                    // 交互
                    "agent_browser_click",
                    "agent_browser_fill",
                    "agent_browser_type",
                    "agent_browser_press",
                    "agent_browser_select",
                    "agent_browser_check",
                    "agent_browser_uncheck",
                    "agent_browser_scroll",
                    // 等待
                    "agent_browser_wait_for_load",
                    "agent_browser_wait_for_selector",
                ],
                cleanupCommand: ["agent-browser", "close", "--all"],
                externalResourceName: "Chrome 浏览器窗口（agent-browser）",
                recoverPatterns: ["Auto-launch failed", "No running Chrome instance"],
                // stdout 是 structuredContent 的完整重复，lifecycle 是启动元数据，LLM 都用不到
                resultNoiseKeys: ["stdout", "stderr", "exitCode", "lifecycle"],
                // agent-browser 每个工具 schema 都拖着的公共高级参数，LLM 从不使用
                schemaNoiseParams: [
                    "restore", "restoreSave", "restoreCheckUrl", "restoreCheckText",
                    "restoreCheckFn", "session", "namespace", "extraArgs",
                    "allowedDomains", "webgpu", "headed", "timeoutMs",
                ],
                pageTextSource: .mcpContentText
            )
        ]
    )
}
#endif
