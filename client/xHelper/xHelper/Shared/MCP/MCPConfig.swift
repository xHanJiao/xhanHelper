#if os(macOS)
import Foundation

// 单个 MCP server 的配置
struct MCPServerConfig {
    let name: String              // server_name，如 "browser"（禁止含 "__"）
    let command: String           // 可执行文件名或绝对路径，如 "agent-browser"
    let args: [String]            // 如 ["mcp"]
    let env: [String: String]?
    let idleTimeoutSecs: TimeInterval // 空闲回收阈值，默认 300
    let description: String
    let toolAllowlist: Set<String>?   // 上报/执行的工具白名单，nil = 不过滤
}

// MCPManager 的配置
struct MCPManagerConfig {
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
                ]
            )
        ]
    )
}
#endif
