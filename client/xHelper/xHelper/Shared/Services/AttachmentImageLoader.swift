import Foundation
import SwiftUI

#if os(iOS)
import UIKit
typealias PlatformImage = UIImage
#else
import AppKit
typealias PlatformImage = NSImage
#endif

/// 图片缩略图缓存 + 鉴权加载。
///
/// 私有图不能走 `AsyncImage`（它加不了 Authorization 头），统一从这里取字节。
/// 同一张图在列表里滚动/重进会话时命中缓存，不重复请求。
@MainActor
enum AttachmentImageLoader {
    private static let cache = NSCache<NSString, PlatformImage>()
    private static var inflight: [String: Task<PlatformImage?, Never>] = [:]

    /// 同步命中缓存（渲染前先看，避免闪烁）
    static func cached(_ id: String) -> PlatformImage? {
        cache.object(forKey: id as NSString)
    }

    /// 从服务端拉取（带 Bearer 鉴权）并缓存
    static func load(id: String, baseURL: String, token: String) async -> PlatformImage? {
        if let hit = cached(id) { return hit }
        if let running = inflight[id] { return await running.value }

        let task = Task<PlatformImage?, Never> {
            let api = APIClient(baseURL: baseURL, token: token)
            guard let data = try? await api.fetchImageData(id: id),
                  let image = PlatformImage(data: data) else { return nil }
            cache.setObject(image, forKey: id as NSString)
            return image
        }
        inflight[id] = task
        let result = await task.value
        inflight[id] = nil
        return result
    }
}

// MARK: - 待发送附件（选图后、上传前）

/// DraftAttachment — 草稿区里的一张待发图（上传成功后由 ChatView 收集 image_id）。
struct DraftAttachment: Identifiable, Equatable {
    let id = UUID()
    let data: Data
    let image: PlatformImage
    let filename: String

    static func == (lhs: DraftAttachment, rhs: DraftAttachment) -> Bool { lhs.id == rhs.id }
}
