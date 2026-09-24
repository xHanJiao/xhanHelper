import Foundation

/// Agent 默认可读写工作区（App 自有，隐式授权，无需用户手动添加）。
///
/// 路径：`~/Library/Application Support/<ToolsDataRoot.name>/workspace`（dev/prod 各一份）
///
/// 只把 `workspace/` 设为隐式读写根，**不含**同级的 `tool_output/`
/// （那是工具结果溢出落盘目录，仅对 fs 工具做只读例外，避免 agent 改写溢出文件）。
/// 工作区在 App 自有目录内，写入内容不在 `PATH`、不被执行。
nonisolated enum AgentWorkspace {
    static var rootPath: String {
        let appSupport = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Library/Application Support")
        return appSupport.appendingPathComponent(ToolsDataRoot.name + "/workspace", isDirectory: true).path
    }

    /// 隐式读写根（`DirectoryAuthorizationManager.isAuthorized` 直接放行）。
    static var implicitReadWriteRoots: [String] { [rootPath] }

    /// 幂等创建目录。
    static func ensureExists() {
        try? FileManager.default.createDirectory(atPath: rootPath, withIntermediateDirectories: true)
    }
}
