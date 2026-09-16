import SwiftUI

/// 图片库网格（右侧栏「图片」页）：缩略图 + 来源/体积 + 删除 + 点击预览大图。
struct ImageListView: View {
    @ObservedObject var viewModel: ImageViewModel
    let baseURL: String
    let token: String

    /// 待确认删除的图（只有"删了会裂图"的才走确认）
    @State private var pendingDelete: ImageAssetSummary?

    private static let thumbMinWidth: CGFloat = 150

    var body: some View {
        PageContainer(
            title: "图片",
            icon: "photo.on.rectangle.angled",
            // 只有首屏空列表才整页转圈：翻页别把已显示的图换成 spinner
            isLoading: viewModel.isLoading && viewModel.items.isEmpty,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.items.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有图片，聊天里发一张试试",
            onRetry: { Task { await viewModel.load() } }
        ) {
            grid
        }
        .task { await viewModel.load() }
        .alert("删除这张图片？", isPresented: isConfirming, presenting: pendingDelete) { item in
            Button("删除", role: .destructive) { Task { await viewModel.delete(id: item.id) } }
            Button("取消", role: .cancel) {}
        } message: { item in
            Text(confirmMessage(item))
        }
    }

    private var isConfirming: Binding<Bool> {
        Binding(get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } })
    }

    private var grid: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: Self.thumbMinWidth), spacing: 10)],
                      spacing: 10) {
                ForEach(viewModel.items) { item in
                    tile(item)
                        .onAppear {
                            viewModel.loadThumb(item.id)
                            if item.id == viewModel.items.last?.id {
                                Task { await viewModel.loadMore() }
                            }
                        }
                }
            }
            .padding(10)
        }
    }

    private func tile(_ item: ImageAssetSummary) -> some View {
        VStack(spacing: 0) {
            ZStack {
                if let thumb = viewModel.thumbs[item.id] {
                    Image(platformImage: thumb)
                        .resizable()
                        .aspectRatio(contentMode: .fill)
                } else {
                    Color.clear.overlay { ProgressView().controlSize(.small) }
                }
            }
            .frame(height: 110)
            .frame(maxWidth: .infinity)
            .background(.quaternary.opacity(0.3))
            .clipped()
            .overlay(alignment: .topTrailing) { deleteButton(item) }

            HStack(spacing: 4) {
                Text(sourceLabel(item.source))
                    .font(.system(size: 10, weight: .medium))
                if item.referenced {
                    Text("使用中")
                        .font(.system(size: 9))
                        .padding(.horizontal, 4)
                        .padding(.vertical, 1)
                        .background(Color.orange.opacity(0.2), in: Capsule())
                }
                Spacer(minLength: 0)
                Text(byteText(item.bytes))
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 4)
        }
        .background(.quaternary.opacity(0.18), in: RoundedRectangle(cornerRadius: 8))
        .contentShape(Rectangle())
        .onTapGesture { presentPreview(item) }
        .help(helpText(item))
    }

    private func deleteButton(_ item: ImageAssetSummary) -> some View {
        Button {
            if needsConfirm(item) {
                pendingDelete = item
            } else {
                Task { await viewModel.delete(id: item.id) }
            }
        } label: {
            Image(systemName: "xmark.circle.fill")
                .font(.system(size: 14))
                .foregroundStyle(.white, .black.opacity(0.45))
        }
        .buttonStyle(.plain)
        .padding(4)
        .help("删除")
    }

    /// 大图预览：全尺寸按需拉（只有点开过的图才进共享缓存）
    private func presentPreview(_ item: ImageAssetSummary) {
        AttachmentImageLoader.shared.load(id: item.id, baseURL: baseURL, token: token)
        LightboxCenter.shared.present {
            ImagePreviewOverlay(item: item, baseURL: baseURL, token: token) {
                LightboxCenter.shared.dismiss()
            }
        }
    }

    // MARK: - 文案

    /// 只有"删了会裂图"才确认：被消息引用，或来自聊天以外的来源（文档/网页/本地）
    private func needsConfirm(_ item: ImageAssetSummary) -> Bool {
        item.referenced || item.source != "chat"
    }

    private func confirmMessage(_ item: ImageAssetSummary) -> String {
        if item.referenced {
            return "这张图已被消息引用，删除后对话里会显示占位。"
        }
        return "这是\(sourceLabel(item.source))用的图，删除后对应页面会裂图。"
    }

    private func sourceLabel(_ source: String) -> String {
        switch source {
        case "chat":  return "聊天"
        case "doc":   return "文档"
        case "local": return "本地"
        case "web":   return "网页"
        default:      return source
        }
    }

    private func byteText(_ bytes: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)
    }

    private func helpText(_ item: ImageAssetSummary) -> String {
        var parts = [sourceLabel(item.source), byteText(item.bytes), String(item.createdAt.prefix(10))]
        if item.referenced { parts.append("已被消息引用") }
        parts.append(item.hasFileId ? "已传 DeepSeek" : "未传 DeepSeek")
        parts.append(item.mime)
        return parts.joined(separator: " · ")
    }
}

/// 预览浮层内容：等共享缓存里的大图到位（`load` 在打开时已经发起）
private struct ImagePreviewOverlay: View {
    let item: ImageAssetSummary
    let baseURL: String
    let token: String
    let onClose: () -> Void

    @ObservedObject private var loader = AttachmentImageLoader.shared

    var body: some View {
        Group {
            if let image = loader.cached(item.id) {
                AttachmentPreviewView(image: image, onClose: onClose)
            } else {
                ProgressView()
                    .controlSize(.large)
                    .frame(width: 320, height: 240)
            }
        }
        .task(id: item.id) {
            loader.load(id: item.id, baseURL: baseURL, token: token)
        }
    }
}
