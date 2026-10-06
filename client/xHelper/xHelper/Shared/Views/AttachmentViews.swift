import SwiftUI

/// 可折叠缩略图行：折叠态单行塞满、放不下的收成「+N」；展开态自适应网格、行数封顶。
/// 草稿托盘与消息图片条共用——两处差异只有尺寸、圆角、对齐和行尾附加格数。
struct CollapsibleThumbRow<Thumb: View, Trailing: View>: View {
    let count: Int
    let thumbSize: CGFloat
    var spacing: CGFloat = 8
    var cornerRadius: CGFloat = 8
    /// 展开态最多几行（超出靠内部滚动）；nil = 不封顶（消息图片条：上限就 20 张，展开就全露出来，
    /// 封 3 行会留个滚动条反而更丑）。托盘必须有封顶，不然 20 张能把输入框顶掉大半
    var maxExpandedRows: Int? = 3
    /// 行尾附加格数（「+」「清空」＝2，消息图片条＝0）。宽度按缩略图边长推，不单独传
    var trailingCells: Int = 0
    /// 折叠态最多露几张（固定上限，不随窗口变宽而增多）；nil = 纯按宽度折叠
    var maxCollapsedThumbs: Int? = nil
    /// 内容整体靠右（消息图片条）还是靠左（输入框托盘）
    var alignTrailing = false
    @Binding var expanded: Bool
    @ViewBuilder let thumb: (Int) -> Thumb
    @ViewBuilder let trailing: () -> Trailing

    @State private var available: CGFloat = 0

    /// 折叠态怎么排（露出几张、要不要「+N」）——算式在 ThumbRowLayout，那里有测试
    private var collapsedRow: (visible: Int, overflow: Bool) {
        ThumbRowLayout.collapsed(
            available: available, thumb: thumbSize, spacing: spacing,
            trailingCells: trailingCells, count: count, maxVisible: maxCollapsedThumbs
        )
    }
    private var overflows: Bool { collapsedRow.overflow }
    private var showGrid: Bool { expanded && overflows }

    var body: some View {
        // 宽度只能从 GeometryReader 拿：它报回的是父级提议尺寸，与内容无关。
        // 反过来（拿自身 frame 的宽度）会被内容污染——HStack 里全是固定尺寸格子，
        // 内容比提议宽时报回的是内容理想宽，于是"图越多量到的越宽、越不折叠"，
        // 还会把这个宽度泄漏给 InputBar.barWidth 把编辑器也撑变形。
        GeometryReader { geo in
            Group {
                if available > 0 {
                    if showGrid { grid } else { row }
                } else {
                    // 首帧还没拿到几何宽度：先空着，绝不能在这一帧按"全部铺开"去画
                    Color.clear
                }
            }
            .frame(maxWidth: .infinity, alignment: alignTrailing ? .trailing : .leading)
            .onChange(of: geo.size.width, initial: true) { _, width in
                guard width != available else { return }
                available = width
            }
        }
        // GeometryReader 纵向也贪婪：不钉住高度它会吸掉整个剩余空间
        .frame(height: ThumbRowLayout.height(
            available: available, thumb: thumbSize, spacing: spacing,
            trailingCells: trailingCells, maxRows: maxExpandedRows,
            count: count, expanded: showGrid
        ))
        // 图少到不再溢出时自动收起，否则会卡在一个只有一格的「展开态」
        .onChange(of: overflows) { _, nowOverflows in
            if !nowOverflows { expanded = false }
        }
    }

    private var row: some View {
        horizontalRow(visible: collapsedRow.visible, overflow: collapsedRow.overflow)
    }

    /// 一行 HStack：按内容定宽（多出来的空档由 Spacer 吃掉），左右交给外层 `frame(alignment:)`。
    /// 网格做不到这一点（见 `grid`），所以单行展开态也走这里。
    private func horizontalRow(visible: Int, overflow: Bool, collapse: Bool = false) -> some View {
        HStack(spacing: spacing) {
            if alignTrailing { Spacer(minLength: 0) }
            ForEach(0..<visible, id: \.self) { index in
                thumb(index)
            }
            if overflow { overflowCell }
            if collapse { collapseCell }
            trailing()
            if !alignTrailing { Spacer(minLength: 0) }
        }
    }

    @ViewBuilder
    private var grid: some View {
        let rows = ThumbRowLayout.expandedRows(
            available: available, thumb: thumbSize, spacing: spacing,
            trailingCells: trailingCells, count: count
        )
        // 一行装得下时不走网格：LazyVGrid 会吃满父级提议宽度、列从前缘起排，多出的空档留在
        // 网格内部尾部 —— 外层 `frame(alignment: .trailing)` 于是成了空操作，展开后整排跑到左边
        // （它自带的 alignment 只管"格子在列内"，不管"列整体靠哪边"）。`rows == 1` 意味着所有格子
        // 一行装得下，HStack 不会溢出
        if rows <= 1 {
            horizontalRow(visible: count, overflow: false, collapse: true)
        } else {
            let content = LazyVGrid(
                columns: [GridItem(.adaptive(minimum: thumbSize, maximum: thumbSize), spacing: spacing)],
                alignment: alignTrailing ? .trailing : .leading,
                spacing: spacing
            ) {
                ForEach(0..<count, id: \.self) { index in
                    thumb(index)
                }
                collapseCell
                trailing()
            }
            .frame(maxWidth: .infinity, alignment: alignTrailing ? .trailing : .leading)

            if let cap = maxExpandedRows, rows > cap {
                ScrollView(.vertical) { content }
                    .frame(
                        height: thumbSize * CGFloat(cap) + spacing * CGFloat(cap - 1)
                    )
            } else {
                content
            }
        }
    }

    private var overflowCell: some View {
        Button {
            withAnimation(.easeOut(duration: 0.2)) { expanded = true }
        } label: {
            cellChrome {
                Text("+\(count - collapsedRow.visible)")
                    .font(.system(size: min(13, thumbSize * 0.22), weight: .medium))
                    .foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
        .help("展开全部 \(count) 张")
    }

    private var collapseCell: some View {
        Button {
            withAnimation(.easeOut(duration: 0.2)) { expanded = false }
        } label: {
            cellChrome {
                Image(systemName: "chevron.up")
                    .font(.system(size: min(14, thumbSize * 0.24), weight: .medium))
                    .foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
        .help("收起")
    }

    /// 「+N」/「收起」格的统一底板
    private func cellChrome<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        ZStack {
            RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                .fill(Color.secondary.opacity(0.12))
            content()
        }
        .frame(width: thumbSize, height: thumbSize)
    }
}

/// 草稿缩略图托盘：输入框下方，可删、可继续加、可一次清空、可只发图。
/// 图多时收成「+N」，点开换行展开。
struct AttachmentTray: View {
    let attachments: [DraftAttachment]
    let onRemove: (DraftAttachment) -> Void
    let onAdd: () -> Void
    /// 一次清空所有待发图
    let onClearAll: () -> Void
    /// 点击缩略图看大图
    let onPreview: (DraftAttachment) -> Void

    private let thumbSize: CGFloat = 56
    @State private var expanded = false

    private var canAddMore: Bool { attachments.count < Layout.maxDraftImages }
    /// 只有一张时点缩略图上的「×」就够了，不必多摆一个「清空」占宽度
    private var showsClearAll: Bool { attachments.count >= 2 }

    var body: some View {
        CollapsibleThumbRow(
            count: attachments.count,
            thumbSize: thumbSize,
            trailingCells: showsClearAll ? 2 : 1,
            expanded: $expanded
        ) { index in
            thumbCell(attachments[index])
        } trailing: {
            addCell
            if showsClearAll { clearAllCell }
        }
    }

    private func thumbCell(_ item: DraftAttachment) -> some View {
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
            // 收在格子内，不能跨在角上：跨角会向上溢出，展开态的 ScrollView 会把徽标上缘裁掉
            .offset(x: -2, y: 2)
        }
    }

    /// 「+」格子：再加一张图
    private var addCell: some View {
        Button(action: onAdd) {
            squareCell {
                Image(systemName: "plus")
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
        .disabled(!canAddMore)
        .help(canAddMore ? "再加一张图" : "最多 \(Layout.maxDraftImages) 张")
    }

    private var clearAllCell: some View {
        Button(action: onClearAll) {
            squareCell {
                Image(systemName: "trash")
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
        .help("清空全部 \(attachments.count) 张")
    }

    /// 行尾各方格的统一底板
    private func squareCell<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        ZStack {
            RoundedRectangle(cornerRadius: 8, style: .continuous)
                .fill(Color.secondary.opacity(0.1))
            content()
        }
        .frame(width: thumbSize, height: thumbSize)
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
    @State private var expanded = false

    var body: some View {
        CollapsibleThumbRow(
            count: imageIds.count,
            thumbSize: thumbSize,
            cornerRadius: 10,
            maxExpandedRows: nil,
            maxCollapsedThumbs: 3,
            alignTrailing: true,
            expanded: $expanded
        ) { index in
            MessageAttachmentThumb(
                imageId: imageIds[index],
                baseURL: baseURL,
                token: token,
                localImage: index == 0 ? localImage : nil,
                size: thumbSize,
                onTap: onPreview
            )
        } trailing: {
            EmptyView()
        }
    }
}

/// 单张消息缩略图：图存在共享存储里（视图重建不丢），缓存未命中才异步拉；拉不到显示占位（图已被清理）。
struct MessageAttachmentThumb: View {
    let imageId: String
    let baseURL: String
    let token: String
    var localImage: PlatformImage? = nil
    let size: CGFloat
    let onTap: (PlatformImage) -> Void

    @ObservedObject private var loader = AttachmentImageLoader.shared

    var body: some View {
        Group {
            if let img = loader.images[imageId] ?? localImage {
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
                        if loader.failed.contains(imageId) {
                            Image(systemName: "photo.badge.exclamationmark")
                                .foregroundStyle(.secondary)
                                .help("图片加载失败（可能已被清理）")
                        } else {
                            ProgressView().controlSize(.small)
                        }
                    }
            }
        }
        .task(id: imageId) {
            guard localImage == nil else { return }
            // 拉不到就一直重试：网络类失败不记终局，而 .task 只跑一次、也没别的状态变化会
            // 触发重渲染 → 切屏回来时被掐断的请求会让缩略图永远转圈。成功或 404 即退出；
            // 视图消失时本任务被取消，自动停。
            while !Task.isCancelled {
                loader.load(id: imageId, baseURL: baseURL, token: token)
                if loader.images[imageId] != nil || loader.failed.contains(imageId) { return }
                try? await Task.sleep(for: .seconds(2))
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
