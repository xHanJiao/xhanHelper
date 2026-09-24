// swift-tools-version: 5.9
import PackageDescription

// FsCore — fs 工具的纯逻辑包（grep 解析/收集/格式化 + 敏感文件判定）。
// 与 app 共享同一份源文件（path 指向 xHelper/Shared/Tools），app 目标不受影响；
// 仅用于 agent 侧 swift test 自验纯逻辑，集成行为仍由用户在 Xcode 中验证。
let package = Package(
    name: "FsCore",
    platforms: [.macOS(.v13)],
    targets: [
        .target(
            name: "FsCore",
            path: "xHelper/Shared",
            sources: ["Tools/GrepParser.swift", "Tools/SensitiveFileBlocker.swift", "Tools/PDFTextCore.swift", "Tools/SubagentResultParser.swift", "Tools/ToolOutputStore.swift", "Tools/FsPath.swift", "Tools/AgentWorkspace.swift", "Tools/DirectoryAuthorizationManager.swift", "Tools/ShellCommandParser.swift", "Tools/ShellCommandDescriber.swift", "Tools/DiffRenderer.swift", "Tools/ToolsDataRoot.swift"]
        ),
        .testTarget(
            name: "FsCoreTests",
            dependencies: ["FsCore"],
            path: "Tests/FsCoreTests"
        ),
        .target(
            name: "ThumbRowLayout",
            path: "xHelper/Shared/Views",
            sources: ["ThumbRowLayout.swift"]
        ),
        .testTarget(
            name: "ThumbRowLayoutTests",
            dependencies: ["ThumbRowLayout"],
            path: "Tests/ThumbRowLayoutTests"
        ),
        // LocalArtifactsCore — 本地产物管线（chart / 文档卡片…）的纯逻辑：
        // artifact 编解码 + producer + 重定位 + 按会话持久化。app 与测试共享同一份源文件。
        .target(
            name: "LocalArtifactsCore",
            path: "xHelper/Shared",
            sources: [
                "LocalArtifacts",
                "Models/LocalToolResultData.swift",
                "Models/ChartData.swift",
                "Services/LocalToolResultStore.swift",
            ]
        ),
        .testTarget(
            name: "LocalArtifactsCoreTests",
            dependencies: ["LocalArtifactsCore"],
            path: "Tests/LocalArtifactsCoreTests"
        ),
        // LocalBackendCore — 本地后端启动器的纯逻辑（密钥校验/env 拼装 + 退出原因归类 + 连通性探测请求构造）。
        .target(
            name: "LocalBackendCore",
            path: "xHelper/Shared",
            sources: ["Services/LocalBackendDiagnostics.swift", "Services/LocalSecrets.swift", "Services/LocalConnectionProbe.swift", "Services/LocalPaths.swift"]
        ),
        .testTarget(
            name: "LocalBackendCoreTests",
            dependencies: ["LocalBackendCore"],
            path: "Tests/LocalBackendCoreTests"
        ),
    ]
)
