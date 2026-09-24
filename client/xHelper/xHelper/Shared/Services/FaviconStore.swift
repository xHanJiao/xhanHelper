import Foundation
import Combine
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// 网站图标缓存：按 host 维度缓存平台图片（macOS=NSImage / iOS=UIImage），供浏览器 tab 条展示。
/// 提取链路：页面导航后注入 JS 读 `<link rel="icon">` → 解析绝对 URL → URLSession 拉取 → 内存缓存 + 磁盘缓存。
/// 磁盘缓存按 host 落到 `LocalPaths.dataRoot/favicons/<host>.png`，重启后从磁盘恢复（收藏夹栏等图标不丢失）。
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

    private init() {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent(LocalPaths.dataRootName + "/favicons", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        cacheDir = dir
        Task { @MainActor [weak self] in
            self?.loadDiskCache()
        }
    }

    func image(forHost host: String) -> FaviconImage? {
        images[host.lowercased()]
    }

    /// 启动时把磁盘缓存读回内存（host 为文件名，天然去重幂等）。
    private func loadDiskCache() {
        let fm = FileManager.default
        guard let files = try? fm.contentsOfDirectory(
            at: cacheDir,
            includingPropertiesForKeys: nil,
            options: [.skipsHiddenFiles]
        ) else { return }
        for file in files {
            guard file.pathExtension.lowercased() == "png" else { continue }
            let key = file.deletingPathExtension().lastPathComponent.lowercased()
            guard !key.isEmpty else { continue }
            if let data = try? Data(contentsOf: file),
               let img = FaviconImage(data: data) {
                images[key] = img
            }
        }
    }

    /// 从页面 href 提取并缓存 favicon。已缓存或正在加载则跳过（host 维度幂等）。
    func loadFavicon(href: String, pageURL: URL, host: String, userAgent: String?) {
        let key = host.lowercased()
        guard images[key] == nil, !loading.contains(key) else { return }
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
        URLSession.shared.dataTask(with: req) { [weak self] data, _, _ in
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
        try? data.write(to: url, options: .atomic)
    }
}
