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
    /// 终局失败（404 / 解码不出）：显示破图占位，不再重试。网络抖动不记
    @Published private(set) var failed: Set<String> = []
    @Published var isLoading = false
    @Published var errorMessage: String?
    /// 从图片库直接上传中（禁用上传按钮 + 显示进度）
    @Published private(set) var isUploading = false

    /// 多选管理态（对齐文档列表：页头换成选择工具条）
    @Published var isSelecting = false
    @Published var selectedIDs: Set<String> = []
    /// 批量删除进行中（防重入 + 禁用工具条按钮）
    @Published private(set) var isDeleting = false

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
        // 重进页面时给上次失败的图再来一次（服务端可能已把脏记录清掉，列表里就不再有它们）
        failed.removeAll()
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

    /// 拉一张缩略图（幂等，格子进入视野时调）。404 / 解码失败记为终局，显示破图占位；
    /// 其余（网络抖动）只记日志、留占位，重进页面会再试
    func loadThumb(_ id: String) {
        guard thumbs[id] == nil, !failed.contains(id), !loadingThumbs.contains(id) else { return }
        loadingThumbs.insert(id)
        Task {
            defer { loadingThumbs.remove(id) }
            do {
                let data = try await apiClient.fetchImageData(id: id)
                guard let cg = ImageCompressor.thumbnail(data, maxPixel: Self.thumbMaxPixel) else {
                    failed.insert(id)
                    return
                }
                store(thumb: cg, id: id)
            } catch {
                LogBuffer.shared.error("ImageViewModel.loadThumb \(id.prefix(8)): \(error)")
                if (error as NSError).code == 404 { failed.insert(id) }
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

    /// 从图片库直接上传若干图片（压缩后走现有上传接口），完成后刷新列表。
    /// 文件选择器给的 URL 需要 security-scoped 访问（沙盒）。
    func upload(urls: [URL]) async {
        guard !urls.isEmpty, !isUploading else { return }
        isUploading = true
        defer { isUploading = false }
        var uploaded = 0
        let total = urls.count
        for url in urls {
            let scoped = url.startAccessingSecurityScopedResource()
            defer { if scoped { url.stopAccessingSecurityScopedResource() } }
            do {
                // 读盘 + 压缩都丢到后台：Data(contentsOf:) 是同步 IO，大图放主线程会卡 UI
                let data = try await Task.detached {
                    let raw = try Data(contentsOf: url)
                    return ImageCompressor.shrink(raw) ?? raw
                }.value
                _ = try await apiClient.uploadImage(data, filename: url.lastPathComponent)
                uploaded += 1
            } catch {
                LogBuffer.shared.error("ImageViewModel.upload \(url.lastPathComponent): \(error)")
            }
        }
        if uploaded > 0 {
            ToastCenter.shared.show(uploaded == total
                ? "已上传 \(uploaded) 张"
                : "已上传 \(uploaded)/\(total) 张，其余失败")
            await load()
        } else {
            ToastCenter.shared.show("图片上传失败")
        }
    }

    /// 删除（服务端删记录 + 文件）。失败回滚并报错。
    func delete(id: String) async {
        guard let removed = items.first(where: { $0.id == id }) else { return }
        withAnimation { items.removeAll { $0.id == id } }
        forgetCaches([id])
        do {
            try await apiClient.deleteImage(id: id)
        } catch {
            LogBuffer.shared.error("ImageViewModel.delete: \(error)")
            withAnimation { items.append(removed) }
            errorMessage = error.localizedDescription
        }
    }

    /// 已删的图不能再从缓存里显示在消息中（大图缓存 + 缩略图）。
    private func forgetCaches(_ ids: some Sequence<String>) {
        for id in ids {
            AttachmentImageLoader.shared.forget(id: id)
            thumbs.removeValue(forKey: id)
        }
    }

    // MARK: - 多选

    func enterSelection() {
        selectedIDs.removeAll()
        isSelecting = true
    }

    func exitSelection() {
        isSelecting = false
        selectedIDs.removeAll()
    }

    func toggleSelection(_ id: String) {
        if selectedIDs.contains(id) {
            selectedIDs.remove(id)
        } else {
            selectedIDs.insert(id)
        }
    }

    /// 全选当前已加载的图（不额外拉全库）。
    func selectAll() {
        selectedIDs = Set(items.map(\.id))
    }

    /// 批量删除所选：循环单张接口（个人规模够用，量大再上批量端点）。
    /// 404 视为已删；非 404 失败的服务端没删掉，留在列表里（别假装成功）。
    /// 全成功弹一次「已删除 N 张」，否则「已删除 x/N 张，其余失败」。
    func deleteSelected() async {
        guard !isDeleting else { return }
        let ids = selectedIDs
        guard !ids.isEmpty else { return }
        isDeleting = true
        defer { isDeleting = false }

        var deleted = Set<String>()
        for id in ids {
            do {
                try await apiClient.deleteImage(id: id)
                deleted.insert(id)
            } catch {
                if (error as NSError).code == 404 {
                    deleted.insert(id) // 已不存在 = 删成功
                } else {
                    LogBuffer.shared.error("ImageViewModel.deleteSelected(\(id)): \(error)")
                }
            }
        }
        withAnimation { items.removeAll { deleted.contains($0.id) } }
        forgetCaches(deleted)
        selectedIDs.removeAll()
        isSelecting = false
        ToastCenter.shared.show(deleted.count == ids.count
            ? "已删除 \(deleted.count) 张"
            : "已删除 \(deleted.count)/\(ids.count) 张，其余失败")
    }
}
