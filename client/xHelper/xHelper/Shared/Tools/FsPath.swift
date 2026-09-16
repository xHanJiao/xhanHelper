import Foundation

/// 路径解析（展开 `~`、标准化 `..`/重复斜杠、解析 symlink 到真实路径）。
/// 被 `FileSystemToolProvider.resolvePath` 与 shell 工具复用；纯逻辑，纳入 FsCore 测试包。
nonisolated enum FsPath {
    static func resolve(_ path: String) -> String {
        var resolved = (path as NSString).expandingTildeInPath
        resolved = (resolved as NSString).standardizingPath
        let url = URL(fileURLWithPath: resolved).resolvingSymlinksInPath()
        let real = url.path
        if FileManager.default.fileExists(atPath: real) {
            return real
        }
        return resolved
    }
}
