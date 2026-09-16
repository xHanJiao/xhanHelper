import Combine
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
///
/// 状态放在这个共享存储里、**不放在视图的 `@State`**：缩略图行会随窗口宽度换布局结构
/// （`CollapsibleThumbRow` 的「首帧 / 折叠行 / 展开网格」三处分支），一换结构整棵子树重建，
/// 视图自己存的图就退回"未加载"、再转一圈——拖窗口时反复重建，看起来就是永远转。
/// 加载归存储拥有后，重建多少次都直接命中；顺带同一张图并发只发一个请求。
@MainActor
final class AttachmentImageLoader: ObservableObject {
    static let shared = AttachmentImageLoader()

    // ponytail: 会话内不回收已解码的图。若某会话塞进几十张全尺寸照片再考虑 LRU 上限，
    // 上限会把"已显示的图回退成转圈"重新引回来，先不做。
    @Published private(set) var images: [String: PlatformImage] = [:]
    /// 终局失败（图已被清理）：显示占位，不再重试。网络类失败不记，下次重建会再试
    @Published private(set) var failed: Set<String> = []

    private var inflight: [String: Task<Void, Never>] = [:]

    private init() {}

    /// 同步读（渲染前先看，避免闪一下占位）
    func cached(_ id: String) -> PlatformImage? { images[id] }

    /// 已经有图时直接种进缓存（上传成功后调用）：刚发出去的图在会话重载后
    /// 不必再走网络，省一次往返，也不会因为重拉失败停在转圈
    func seed(id: String, image: PlatformImage) {
        guard images[id] == nil else { return }
        images[id] = image
        failed.remove(id)
    }

    /// 拉取并缓存。视图消失不打断（任务归存储所有，不随视图生命周期取消）
    func load(id: String, baseURL: String, token: String) {
        guard images[id] == nil, !failed.contains(id), inflight[id] == nil else { return }
        guard !baseURL.isEmpty, !token.isEmpty else { return }

        inflight[id] = Task { [weak self] in
            let api = APIClient(baseURL: baseURL, token: token)
            var loaded: PlatformImage?
            // 终局失败（不再重试）：图已被清理，或字节解不开
            var terminal = false
            do {
                let data = try await api.fetchImageData(id: id)
                loaded = PlatformImage(data: data)
                terminal = loaded == nil
            } catch {
                terminal = (error as NSError).code == 404
            }
            guard let self else { return }
            if let loaded {
                self.images[id] = loaded
            } else if terminal {
                self.failed.insert(id)
            }
            self.inflight[id] = nil
        }
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
