import Foundation
import Combine
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// 网站图标缓存：按 host 维度缓存平台图片（macOS=NSImage / iOS=UIImage），供浏览器 tab 条展示。
/// 提取链路：页面就绪后注入 JS 读 `<link rel="icon">` → 解析绝对 URL → URLSession 拉取 → 内存缓存 + 磁盘缓存。
/// 磁盘缓存按 host 落到 `LocalPaths.dataRoot/favicons/v<版本>/<host>.png`，重启后从磁盘恢复（收藏夹栏等图标不丢失）。
#if os(macOS)
typealias FaviconImage = NSImage
#else
typealias FaviconImage = UIImage
#endif

@MainActor
final class FaviconStore: ObservableObject {
    static let shared = FaviconStore()

    @Published private(set) var images: [String: FaviconImage] = [:]
    private var loading: Set<String> = []
    private let cacheDir: URL

    /// 缓存 schema 版本：key 规则或落盘结构变更时 +1，启动即丢弃旧版本缓存，避免脏图复活
    /// （v2 修的是「上一页图标写到新 host」串味，见 docs/bugs/018-favicon-cache-poisoning）。
    private static let cacheSchemaVersion = 2

    private init() {
        let root = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent(LocalPaths.dataRootName + "/favicons", isDirectory: true)
        let dir = root.appendingPathComponent("v\(Self.cacheSchemaVersion)", isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        } catch {
            LogBuffer.shared.warning("[Favicon] 创建缓存目录失败 \(dir.path): \(error.localizedDescription)")
        }
        Self.purgeStaleCache(root: root, keeping: dir)
        cacheDir = dir
        Task { @MainActor [weak self] in
            self?.loadDiskCache()
        }
    }

    /// 删掉非当前版本的缓存：旧版无版本目录时散落在 root 的 `*.png`，以及历史 `vN/` 目录。
    /// 纯缓存，删失败只记录、下次启动重试，不影响启动。
    private static func purgeStaleCache(root: URL, keeping current: URL) {
        let fm = FileManager.default
        let entries: [URL]
        do {
            entries = try fm.contentsOfDirectory(
                at: root,
                includingPropertiesForKeys: [.isDirectoryKey],
                options: [.skipsHiddenFiles]
            )
        } catch {
            LogBuffer.shared.warning("[Favicon] 枚举旧缓存失败: \(error.localizedDescription)")
            return
        }
        for entry in entries where entry.lastPathComponent != current.lastPathComponent {
            let isDir: Bool
            do {
                isDir = try entry.resourceValues(forKeys: [.isDirectoryKey]).isDirectory ?? false
            } catch {
                LogBuffer.shared.warning("[Favicon] 读取缓存项类型失败 \(entry.lastPathComponent): \(error.localizedDescription)")
                continue
            }
            let stale = isDir ? entry.lastPathComponent.hasPrefix("v") : entry.pathExtension.lowercased() == "png"
            guard stale else { continue }
            do {
                try fm.removeItem(at: entry)
            } catch {
                LogBuffer.shared.warning("[Favicon] 清理旧缓存失败 \(entry.lastPathComponent): \(error.localizedDescription)")
            }
        }
    }

    func image(forHost host: String) -> FaviconImage? {
        images[host.lowercased()]
    }

    /// 启动时把磁盘缓存读回内存（host 为文件名，天然去重幂等）。
    private func loadDiskCache() {
        let fm = FileManager.default
        let files: [URL]
        do {
            files = try fm.contentsOfDirectory(
                at: cacheDir,
                includingPropertiesForKeys: nil,
                options: [.skipsHiddenFiles]
            )
        } catch {
            LogBuffer.shared.warning("[Favicon] 枚举磁盘缓存失败: \(error.localizedDescription)")
            return
        }
        for file in files {
            guard file.pathExtension.lowercased() == "png" else { continue }
            let key = file.deletingPathExtension().lastPathComponent.lowercased()
            guard !key.isEmpty else { continue }
            do {
                let data = try Data(contentsOf: file)
                if let img = FaviconImage(data: data) {
                    images[key] = img
                } else {
                    LogBuffer.shared.diag("[Favicon] 磁盘缓存无法解码为图片，跳过: \(file.lastPathComponent)")
                }
            } catch {
                LogBuffer.shared.diag("[Favicon] 读取磁盘缓存失败 \(file.lastPathComponent): \(error.localizedDescription)")
            }
        }
    }

    /// 从页面 href 提取并缓存 favicon。同 host 并发只跑一次；允许后续正确提取覆盖旧图，
    /// 避免一次提取错误被永久锁死（见 docs/bugs/018-favicon-cache-poisoning）。
    func loadFavicon(href: String, pageURL: URL, host: String, userAgent: String?) {
        let key = host.lowercased()
        guard !loading.contains(key) else { return }
        guard let abs = URL(string: href, relativeTo: pageURL)?.absoluteURL,
              let scheme = abs.scheme?.lowercased(),
              scheme == "http" || scheme == "https"
        else { return }

        loading.insert(key)
        var req = URLRequest(url: abs)
        req.timeoutInterval = 10
        if let ua = userAgent, !ua.isEmpty {
            req.setValue(ua, forHTTPHeaderField: "User-Agent")
        }
        URLSession.shared.dataTask(with: req) { [weak self] data, _, error in
            if let error {
                LogBuffer.shared.warning("[Favicon] 拉取失败 \(abs.absoluteString): \(error.localizedDescription)")
            }
            let img = data.flatMap { FaviconImage(data: $0) }
            Task { @MainActor in
                self?.loading.remove(key)
                if let img, let data {
                    self?.images[key] = img
                    self?.persistToDisk(key: key, data: data)
                }
            }
        }.resume()
    }

    private func persistToDisk(key: String, data: Data) {
        let url = cacheDir.appendingPathComponent("\(key).png")
        do {
            try data.write(to: url, options: .atomic)
        } catch {
            LogBuffer.shared.warning("[Favicon] 写磁盘缓存失败 \(key): \(error.localizedDescription)")
        }
    }
}
