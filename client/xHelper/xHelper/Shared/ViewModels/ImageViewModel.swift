import Combine
import CoreGraphics
import Foundation
import SwiftUI

#if os(iOS)
import UIKit
#else
import AppKit
#endif

/// 图片库（右侧栏「图片」页）：分页列表 + 缩略图缓存 + 删除。
///
/// 缩略图**不复用 `AttachmentImageLoader`**：那边存的是全尺寸解码图、会话内不回收，
/// 一页 50 张 2048px 解码就是几百 MB（它服务的是消息里几张图和点开的大图）。
/// 这里按 id 拉字节后立刻降到 256px 再存，条目数封顶。
@MainActor
final class ImageViewModel: ObservableObject {
    @Published private(set) var items: [ImageAssetSummary] = []
    /// 256px 缩略图（按 id），网格渲染前先查这个
    @Published private(set) var thumbs: [String: PlatformImage] = [:]
    @Published var isLoading = false
    @Published var errorMessage: String?

    private static let pageSize = 50
    private static let thumbMaxPixel = 256
    /// ponytail: 超上限按插入序丢最旧的。真要精细内存回收再上 NSCache/LRU
    private static let thumbCap = 300

    private let apiClient: APIClient
    private var currentOffset = 0
    private var hasMore = true
    private var loadingThumbs: Set<String> = []

    init(apiClient: APIClient) {
        self.apiClient = apiClient
    }

    func load() async {
        isLoading = true
        currentOffset = 0
        hasMore = true
        do {
            let resp = try await apiClient.listImages(limit: Self.pageSize)
            items = resp.items
            hasMore = items.count < resp.total
            errorMessage = nil
        } catch {
            LogBuffer.shared.error("ImageViewModel.load: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    func loadMore() async {
        guard hasMore, !isLoading else { return }
        isLoading = true
        currentOffset += Self.pageSize
        do {
            let resp = try await apiClient.listImages(limit: Self.pageSize, offset: currentOffset)
            items.append(contentsOf: resp.items)
            hasMore = items.count < resp.total
        } catch {
            LogBuffer.shared.error("ImageViewModel.loadMore: \(error)")
            errorMessage = error.localizedDescription
        }
        isLoading = false
    }

    /// 拉一张缩略图（幂等，格子进入视野时调）。失败不报错：留占位，重进页面会再试
    func loadThumb(_ id: String) {
        guard thumbs[id] == nil, !loadingThumbs.contains(id) else { return }
        loadingThumbs.insert(id)
        Task {
            defer { loadingThumbs.remove(id) }
            do {
                let data = try await apiClient.fetchImageData(id: id)
                guard let cg = ImageCompressor.thumbnail(data, maxPixel: Self.thumbMaxPixel) else { return }
                store(thumb: cg, id: id)
            } catch {
                LogBuffer.shared.error("ImageViewModel.loadThumb \(id.prefix(8)): \(error)")
            }
        }
    }

    private func store(thumb cg: CGImage, id: String) {
        #if os(iOS)
        thumbs[id] = UIImage(cgImage: cg)
        #else
        thumbs[id] = NSImage(cgImage: cg, size: NSSize(width: cg.width, height: cg.height))
        #endif
        if thumbs.count > Self.thumbCap, let oldest = thumbs.keys.first(where: { $0 != id }) {
            thumbs.removeValue(forKey: oldest)
        }
    }

    /// 删除（服务端删记录 + 文件）。失败回滚并报错。
    func delete(id: String) async {
        guard let removed = items.first(where: { $0.id == id }) else { return }
        withAnimation { items.removeAll { $0.id == id } }
        // 已删的图不能再从缓存里显示在消息中
        AttachmentImageLoader.shared.forget(id: id)
        thumbs.removeValue(forKey: id)
        do {
            try await apiClient.deleteImage(id: id)
        } catch {
            LogBuffer.shared.error("ImageViewModel.delete: \(error)")
            withAnimation { items.append(removed) }
            errorMessage = error.localizedDescription
        }
    }
}
