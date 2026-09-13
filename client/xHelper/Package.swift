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
            path: "xHelper/Shared/Tools",
            sources: ["GrepParser.swift", "SensitiveFileBlocker.swift", "PDFTextCore.swift", "SubagentResultParser.swift"]
        ),
        .testTarget(
            name: "FsCoreTests",
            dependencies: ["FsCore"],
            path: "Tests/FsCoreTests"
        ),
    ]
)
