import Foundation

/// tools 层的数据根目录名（dev/prod 分环境：Debug = dev 沙盒）。
///
/// **为什么这里还有一份**：`LocalPaths` 与本文件分属不同的 SwiftPM target，
/// 而 SwiftPM 不允许同一个文件进多个 target（会报 overlapping sources）。
/// tools 层的文件（`AgentWorkspace` / `ToolOutputStore`）必须能在 FsCore target 里单独编译，
/// 所以这份判定在 tools 层本地保留一份；app 启动时 `LocalBackendManager` 会断言
/// 它与 `LocalPaths.dataRootName` / `LocalToolResultStore.dataRootName` 三者一致（防漂移）。
nonisolated enum ToolsDataRoot {
    static var name: String {
        #if DEBUG
        "xhanHelper-dev"
        #else
        "xhanHelper"
        #endif
    }
}
