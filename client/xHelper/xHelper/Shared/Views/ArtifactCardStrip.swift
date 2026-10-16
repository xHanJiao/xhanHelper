import SwiftUI

/// 卡片条里卡片的统一尺寸。条子按**固定卡宽**算排布，卡片视图也得按同一尺寸画——
/// 调用方（`MessageListView`）和条子的默认值都用它，免得两处各写一个 240。
enum ArtifactCardMetrics {
    static let width: CGFloat = 240
    static let height: CGFloat = 92
    /// 卡片条展开后过多久自动收起（点开扫一眼就够；条子里没有「收起」控件——展开内容一定比
    /// 可视宽更宽，尾部放什么都得滑到底才够得着）。放这里是因为泛型类型里不能有 static 存储属性。
    static let autoCollapseDelay: Duration = .seconds(8)
}

/// 聊天里「卡片类本地产物」的横向容器。
///
/// 多张卡不再竖向叠：按可用宽度**自适应收起**（塞得下几张露几张，剩下的收成一个窄的「＋N」格），
/// 点它展开成**横向可滑**的一排完整卡。单张不走这里——调用方直接渲染那张卡，与现状一模一样。
///
/// **没有「收起」控件**：展开态的内容必定比可视宽更宽（能展开就说明卡片放不下），尾部再放什么都
/// 得滑到底才够得着，所以改为**展开一段时间后自动收起**（`ArtifactCardMetrics.autoCollapseDelay`）。
///
/// 通用：条子只管排布与收放，卡片内容由 `card` 闭包给出；任何未来的「卡片类」产物都能塞进来。
///
/// 两个几何前提（别改）：卡片**等宽等高**。等宽是为了复用 `ThumbRowLayout` 的折叠算式
/// （它按统一格宽算）；等高是因为 GeometryReader 纵向贪婪、高度必须钉死，也让一排卡看着齐。
struct ArtifactCardStrip<Item: Identifiable, Card: View>: View {
    let items: [Item]
    var cardWidth: CGFloat = ArtifactCardMetrics.width
    var cardHeight: CGFloat = ArtifactCardMetrics.height
    var cardCornerRadius: CGFloat = 11
    var spacing: CGFloat = 10
    /// 「＋N」窄格宽度。窄窗口下会被压得更窄（见 `effectiveTileWidth`），不会为了放它而少露卡片。
    var tileWidth: CGFloat = 72
    @Binding var expanded: Bool
    @ViewBuilder let card: (Item) -> Card

    @State private var available: CGFloat = 0
    @State private var autoCollapseTask: Task<Void, Never>?

    /// 尾格实际宽度：窄窗口里让出「一张卡 + 间距」之后剩多少就用多少
    /// （与 `ThumbRowLayout.collapsed` 同一处钳制，两边的算式必须一致）。
    private var effectiveTileWidth: CGFloat {
        max(0, min(tileWidth, available - cardWidth - spacing))
    }

    /// 收起态露几张、要不要「＋N」——算式在 `ThumbRowLayout.collapsed`（已有单测，保证至少露一张）。
    private var collapsedRow: (visible: Int, overflow: Bool) {
        ThumbRowLayout.collapsed(
            available: available, cell: cardWidth, spacing: spacing,
            trailingCell: tileWidth, count: items.count
        )
    }
    private var overflows: Bool { collapsedRow.overflow }
    private var showExpanded: Bool { expanded && overflows }

    var body: some View {
        // 宽度只能从 GeometryReader 拿：拿自身 frame 的宽度会被内容污染（图越多量到的越宽、
        // 越不折叠），见 `CollapsibleThumbRow` 同款注释。
        GeometryReader { geo in
            Group {
                if available > 0 {
                    if showExpanded { expandedRow } else { collapsedRowView }
                } else {
                    // 首帧还没量到宽度：先空着，绝不能按「全部铺开」去画
                    Color.clear
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .onChange(of: geo.size.width, initial: true) { _, w in
                guard w != available else { return }
                available = w
            }
        }
        // GeometryReader 纵向也贪婪：不钉住高度它会吸掉整个剩余空间
        .frame(height: cardHeight)
        // 窗口变宽后不再溢出 → 自动回收起态，否则会卡在一个只有几格的「假展开」
        .onChange(of: overflows) { _, nowOverflows in
            if !nowOverflows { expanded = false }
        }
        // 展开一会儿自动收起（点开扫一眼就够；**没有手动收起控件**——展开内容一定比可视宽更宽，
        // 尾部放什么都得滑到底才够得着）。收起态不计时；再展开会重新起算（onChange 先取消旧任务）。
        .onChange(of: showExpanded, initial: true) { _, nowExpanded in
            autoCollapseTask?.cancel()
            autoCollapseTask = nil
            guard nowExpanded else { return }
            autoCollapseTask = Task {
                try? await Task.sleep(for: ArtifactCardMetrics.autoCollapseDelay)
                guard !Task.isCancelled else { return }
                withAnimation(.easeOut(duration: 0.2)) { expanded = false }
            }
        }
    }

    private var collapsedRowView: some View {
        HStack(spacing: spacing) {
            ForEach(Array(items.prefix(collapsedRow.visible))) { item in
                card(item)
            }
            if overflows, effectiveTileWidth > 0 { moreTile }
        }
    }

    private var expandedRow: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: spacing) {
                ForEach(items) { item in
                    card(item)
                }
            }
        }
    }

    /// 「＋N」：淡底 + 虚线描边（跟真卡片区分开）。只写数字不带单位——窄格里容不下
    private var moreTile: some View {
        Button {
            withAnimation(.easeOut(duration: 0.2)) { expanded = true }
        } label: {
            tile(width: effectiveTileWidth) {
                Text("＋\(items.count - collapsedRow.visible)")
                    .font(.system(size: 14, weight: .medium))
                    .foregroundColor(Color("assistantText").opacity(0.7))
            }
        }
        .buttonStyle(.plain)
        .help("展开全部 \(items.count) 张")
    }

    private func tile<C: View>(width: CGFloat, @ViewBuilder _ content: () -> C) -> some View {
        ZStack {
            RoundedRectangle(cornerRadius: cardCornerRadius)
                .fill(Color.secondary.opacity(0.10))
            RoundedRectangle(cornerRadius: cardCornerRadius)
                .strokeBorder(style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
                .foregroundColor(Color.secondary.opacity(0.35))
            content()
        }
        .frame(width: width, height: cardHeight)
    }
}

// MARK: - Previews

#if DEBUG
/// #Preview 里没有 @State，包一层拿绑定（比 @Previewable 更省事）
private struct ArtifactCardStripPreview: View {
    let refs: [DocumentRef]
    var width: CGFloat = 620
    @State var expanded: Bool

    var body: some View {
        ArtifactCardStrip(items: refs, expanded: $expanded) { ref in
            DocumentRefCard(ref: ref, width: ArtifactCardMetrics.width, height: ArtifactCardMetrics.height, onOpen: {})
        }
        .padding(.horizontal, 24)
        .padding(.vertical, 20)
        .frame(width: width)
        .background(Color("chatBackground"))
    }
}

private let previewRefs: [DocumentRef] = [
    DocumentRef(id: "1", title: "リオグランデ西新宿 · 入住与落地指南", preview: "**10/17 入住（15:00 后）— 10/21 退房，房间 102。** 现场照着做就行。"),
    DocumentRef(id: "2", title: "东京行程 · 落地动线", preview: "成田 → 新宿：N'EX 直达，出站东口步行 7 分钟。行李多就先寄存。"),
    DocumentRef(id: "3", title: "设备操作页", preview: "空调/热水器的遥控说明。不会用问房东。"),
    DocumentRef(id: "4", title: "周边吃饭清单", preview: "出站南口三家：一家荞麦、一家烧鸟、一家 24h 定食。"),
]

#Preview("3 张 · 收起") { ArtifactCardStripPreview(refs: Array(previewRefs.prefix(3)), expanded: false) }
#Preview("3 张 · 展开") { ArtifactCardStripPreview(refs: Array(previewRefs.prefix(3)), expanded: true) }
#Preview("二阶窄窗（280）· 收起") { ArtifactCardStripPreview(refs: previewRefs, width: 280, expanded: false) }
#Preview("窄窗（420）· 收起") { ArtifactCardStripPreview(refs: previewRefs, width: 420, expanded: false) }
#endif
