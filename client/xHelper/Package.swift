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
            sources: ["Tools/GrepParser.swift", "Tools/SensitiveFileBlocker.swift", "Tools/PDFTextCore.swift", "Tools/SubagentResultParser.swift", "Tools/ToolOutputStore.swift", "Tools/FsPath.swift", "Tools/AgentWorkspace.swift", "Tools/DirectoryAuthorizationManager.swift", "Tools/ShellCommandParser.swift", "Tools/ShellCommandDescriber.swift", "Tools/DiffRenderer.swift", "Tools/ToolConfirmPolicy.swift", "Tools/ToolsDataRoot.swift", "Tools/CalendarEventCore.swift"]
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
        // DocumentStaleGate — 「正文疑似过旧 → 自动拉一次新正文」的闸门（纯状态，无视图）。
        // 它是防死循环的那道闸，抽出来让 swift test 能覆盖（见 Tests/DocumentStaleGateTests）。
        .target(
            name: "DocumentStaleGate",
            path: "xHelper/Shared/Views",
            sources: ["DocumentStaleGate.swift"]
        ),
        .testTarget(
            name: "DocumentStaleGateTests",
            dependencies: ["DocumentStaleGate"],
            path: "Tests/DocumentStaleGateTests"
        ),
        // TopBarLayoutCore — 顶栏自适应降级的纯逻辑（按可用宽选档 + 各档内在宽）。
        // 与 app 共享同一份源文件（含 Layout 宽度常量）；视图只负责照档位渲染。
        .target(
            name: "TopBarLayoutCore",
            path: "xHelper/Shared",
            sources: ["Views/TopBarLayout.swift", "Views/LayoutConstants.swift"]
        ),
        .testTarget(
            name: "TopBarLayoutCoreTests",
            dependencies: ["TopBarLayoutCore"],
            path: "Tests/TopBarLayoutCoreTests"
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
            sources: ["Services/LocalBackendDiagnostics.swift", "Services/LocalSecrets.swift", "Services/LocalConnectionProbe.swift", "Services/LocalPaths.swift", "Services/LocalBackup.swift"]
        ),
        .testTarget(
            name: "LocalBackendCoreTests",
            dependencies: ["LocalBackendCore"],
            path: "Tests/LocalBackendCoreTests"
        ),
        // ActivityCore — 客户端活动埋点的纯逻辑：事件 DTO + JSONL 分段落盘 + 按段搬运编排。
        // 上传传输用闭包注入，故测试不碰网络；app 目标共享同一份源文件。
        .target(
            name: "ActivityCore",
            path: "xHelper/Shared",
            sources: [
                "Services/ActivityEvent.swift",
                "Services/ActivityEventTypes.swift",
                "Services/ActivityUrls.swift",
                "Services/ActivityStore.swift",
                "Services/ActivityRecorder.swift",
                "Services/ActivityPresenceState.swift",
                "Services/ActivityInputState.swift",
                "Services/ActivityBrowserState.swift",
            ]
        ),
        .testTarget(
            name: "ActivityCoreTests",
            dependencies: ["ActivityCore"],
            path: "Tests/ActivityCoreTests"
        ),
        // ImageCompressorCore — 图片压缩的纯逻辑（降采样 + JPEG 编码），窗口底图导入用。
        .target(
            name: "ImageCompressorCore",
            path: "xHelper/Shared",
            sources: ["Services/ImageCompressor.swift"]
        ),
        .testTarget(
            name: "ImageCompressorCoreTests",
            dependencies: ["ImageCompressorCore"],
            path: "Tests/ImageCompressorCoreTests"
        ),
        // SSELineDecoderCore — 字节流按 \n 切行（块边界切在多字节 UTF-8 中间也不丢事件；
        // 坏字节只替换）。app 目标共享同一份源文件。
        // 依赖提醒：若把 Tools/FileSystemToolProvider.swift 加进 FsCore，须同时把
        // Services/SSELineDecoder.swift 加进去，否则 swift test 找不到符号。
        .target(
            name: "SSELineDecoderCore",
            path: "xHelper/Shared",
            sources: ["Services/SSELineDecoder.swift"]
        ),
        .testTarget(
            name: "SSELineDecoderCoreTests",
            dependencies: ["SSELineDecoderCore"],
            path: "Tests/SSELineDecoderCoreTests"
        ),
        // ArtifactHTMLCacheCore — 灵阅正文磁盘缓存（纯文件 IO，不依赖 SwiftUI/LogBuffer）。
        // 目录/上限/日志由调用方注入，测试用临时目录 + 小上限覆盖淘汰与清理。
        .target(
            name: "ArtifactHTMLCacheCore",
            path: "xHelper/Shared",
            sources: ["Services/ArtifactHTMLCache.swift"]
        ),
        .testTarget(
            name: "ArtifactHTMLCacheCoreTests",
            dependencies: ["ArtifactHTMLCacheCore"],
            path: "Tests/ArtifactHTMLCacheCoreTests"
        ),
        // ChatWireCore — 聊天线协议里可纯逻辑化的部分（目前是 tool_call/tool_result 配对规则）。
        // 服务端事件顺序不保证与调用顺序一致，配对必须按 id；规则抽出来让 swift test 钉住。
        .target(
            name: "ChatWireCore",
            path: "xHelper/Shared",
            sources: ["ViewModels/ToolCallPairing.swift"]
        ),
        .testTarget(
            name: "ChatWireCoreTests",
            dependencies: ["ChatWireCore"],
            path: "Tests/ChatWireCoreTests"
        ),
    ]
)
