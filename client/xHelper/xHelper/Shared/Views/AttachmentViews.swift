import SwiftUI

/// 草稿缩略图托盘：输入框下方，可删、可继续加、可只发图。
struct AttachmentTray: View {
    let attachments: [DraftAttachment]
    /// 上传中：托盘上显示进度态，发送按钮禁用
    var isUploading: Bool = false
    let onRemove: (DraftAttachment) -> Void
    let onAdd: () -> Void
    /// 点击缩略图看大图
    let onPreview: (DraftAttachment) -> Void

    private let thumbSize: CGFloat = 56

    var body: some View {
        HStack(spacing: 8) {
            ForEach(attachments) { item in
                ZStack(alignment: .topTrailing) {
                    Button {
                        onPreview(item)
                    } label: {
                        Image(platformImage: item.image)
                            .resizable()
                            .aspectRatio(contentMode: .fill)
                            .frame(width: thumbSize, height: thumbSize)
                            .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                            .overlay(
                                RoundedRectangle(cornerRadius: 8, style: .continuous)
                                    .stroke(Color.secondary.opacity(0.25), lineWidth: 0.5)
                            )
                    }
                    .buttonStyle(.plain)
                    .help(item.filename)

                    Button {
                        onRemove(item)
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.system(size: 14))
                            .foregroundStyle(.white, .black.opacity(0.6))
                    }
                    .buttonStyle(.plain)
                    .offset(x: 5, y: -5)
                }
            }

            Button(action: onAdd) {
                Image(systemName: "plus")
                    .font(.system(size: 15, weight: .medium))
                    .frame(width: thumbSize, height: thumbSize)
                    .foregroundStyle(.secondary)
                    .background(
                        RoundedRectangle(cornerRadius: 8, style: .continuous)
                            .fill(Color.secondary.opacity(0.1))
                    )
            }
            .buttonStyle(.plain)
            // 上传中加进来的图不会被本次上传带上，只会残留在托盘里
            .disabled(isUploading)
            .help("再加一张图")

            if isUploading {
                ProgressView()
                    .controlSize(.small)
                    .padding(.leading, 2)
            }

            Spacer(minLength: 0)
        }
    }
}

/// 消息里的图片条（发出去之后回看用）
struct MessageAttachmentStrip: View {
    let imageIds: [String]
    let baseURL: String
    let token: String
    /// 刚发出、尚未从服务端拉回时直接用内存图（省一次往返 + 不闪）
    var localImage: PlatformImage? = nil
    let onPreview: (PlatformImage) -> Void

    private let thumbSize: CGFloat = 88

    var body: some View {
        HStack(spacing: 6) {
            ForEach(Array(imageIds.enumerated()), id: \.offset) { index, id in
                MessageAttachmentThumb(
                    imageId: id,
                    baseURL: baseURL,
                    token: token,
                    localImage: index == 0 ? localImage : nil,
                    size: thumbSize,
                    onTap: onPreview
                )
            }
        }
    }
}

/// 单张消息缩略图：命中缓存直接显示，否则异步拉取；拉不到显示占位（图已被清理）。
struct MessageAttachmentThumb: View {
    let imageId: String
    let baseURL: String
    let token: String
    var localImage: PlatformImage? = nil
    let size: CGFloat
    let onTap: (PlatformImage) -> Void

    @State private var image: PlatformImage?
    @State private var failed = false

    var body: some View {
        Group {
            if let img = image ?? localImage {
                Button {
                    onTap(img)
                } label: {
                    Image(platformImage: img)
                        .resizable()
                        .aspectRatio(contentMode: .fill)
                        .frame(width: size, height: size)
                        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                        .overlay(
                            RoundedRectangle(cornerRadius: 10, style: .continuous)
                                .stroke(Color.secondary.opacity(0.25), lineWidth: 0.5)
                        )
                }
                .buttonStyle(.plain)
            } else {
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .fill(Color.secondary.opacity(0.12))
                    .frame(width: size, height: size)
                    .overlay {
                        if failed {
                            Image(systemName: "photo.badge.exclamationmark")
                                .foregroundStyle(.secondary)
                                .help("图片已被清理")
                        } else {
                            ProgressView().controlSize(.small)
                        }
                    }
            }
        }
        .task(id: imageId) {
            guard image == nil, localImage == nil else { return }
            if let hit = AttachmentImageLoader.cached(imageId) {
                image = hit
                return
            }
            if !baseURL.isEmpty, !token.isEmpty,
               let loaded = await AttachmentImageLoader.load(id: imageId, baseURL: baseURL, token: token) {
                image = loaded
            } else {
                failed = true
            }
        }
    }
}

/// 大图预览（sheet 内容）：适应窗口，Esc/关闭按钮退出。
struct AttachmentPreviewView: View {
    let image: PlatformImage
    let onClose: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Spacer()
                Button {
                    onClose()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 20))
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .keyboardShortcut(.cancelAction)
            }
            .padding(10)

            Image(platformImage: image)
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding(.horizontal, 12)
                .padding(.bottom, 12)
        }
        .frame(minWidth: 480, minHeight: 360)
    }
}

// MARK: - 平台适配

extension Image {
    init(platformImage: PlatformImage) {
        #if os(iOS)
        self.init(uiImage: platformImage)
        #else
        self.init(nsImage: platformImage)
        #endif
    }
}
