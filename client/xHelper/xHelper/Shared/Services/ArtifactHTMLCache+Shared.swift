import Foundation

extension ArtifactHTMLCache {
    /// 灵阅正文缓存（app 侧单例）。目录与日志走项目约定，核心文件本身不依赖它们。
    static let shared = ArtifactHTMLCache(dir: LocalPaths.artifactsCacheDir) {
        LogBuffer.shared.error($0)
    }
}
