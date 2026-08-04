import Foundation
import Combine
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// 网站图标缓存：按 host 维度缓存平台图片（macOS=NSImage / iOS=UIImage），供浏览器 tab 条展示。
/// 提取链路：页面导航后注入 JS 读 `<link rel="icon">` → 解析绝对 URL → URLSession 拉取 → 内存缓存。
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

    private init() {}

    func image(forHost host: String) -> FaviconImage? {
        images[host.lowercased()]
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
                if let img {
                    self?.images[key] = img
                }
            }
        }.resume()
    }
}
