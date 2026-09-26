import Foundation

/// 进程级单例：目录落 `LocalPaths.dataRoot/activity`（本地模式才被启动，iOS/云端不启用）。
///
/// 独立成文件、**不编进 SwiftPM 的 `ActivityCore` target**（`LocalPaths.swift` 已属于
/// `LocalBackendCore`，同文件不能进两个 target）。纯逻辑测试直接用 `ActivityRecorder(directory:)`。
extension ActivityRecorder {
    static let shared = ActivityRecorder(
        directory: LocalPaths.dataRoot.appendingPathComponent("activity", isDirectory: true)
    )
}
