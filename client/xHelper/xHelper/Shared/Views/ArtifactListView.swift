import SwiftUI

/// 右栏「灵阅」：聊天里生成过的 HTML 产物（标题 + 摘要 + 日期），点一条在右栏开 tab。
///
/// 观感与文档列表同款（`PageContainer` + `CardGridView`），但数据源是 artifacts 表、
/// 跟消息无关：会话归档了这里照样能翻（见 docs/design/html-artifact §9.5）。
///
/// 多选（2026-10-30 与文档/图片统一）：状态在 `ArtifactViewModel`，入口图标与工具条都在
/// `BrowsePanelView` 的页头，这里只负责卡片本身（勾选圈 / 点击切换 / `⋯` 菜单）。
struct ArtifactListView: View {
    @ObservedObject var viewModel: ArtifactViewModel

    /// 待确认的单条删除（批量删除的确认框在 `BrowsePanelView` 页头那条链路上）
    @State private var pendingDeletion: ArtifactSummary?

    var body: some View {
        Group {
            #if os(macOS)
            if viewModel.mode == .cloud {
                cloudContent
            } else {
                listContent
            }
            #else
            listContent
            #endif
        }
        .task { await viewModel.loadIfStale() }
        .alert(
            "删除页面？",
            isPresented: Binding(get: { pendingDeletion != nil }, set: { if !$0 { pendingDeletion = nil } }),
            presenting: pendingDeletion
        ) { item in
            Button("删除", role: .destructive) { Task { await viewModel.delete(id: item.id) } }
            Button("取消", role: .cancel) {}
        } message: { item in
            Text("《\(item.title.isEmpty ? "未命名页面" : item.title)》将被删除，不能恢复；已分享的链接会立即失效。")
        }
    }

    private var listContent: some View {
        VStack(spacing: 0) {
            if !viewModel.activeTags.isEmpty {
                activeTagBar
            }
            listPage
        }
    }

    /// 列表模式的标签筛选条：交集可能多标签，逐 chip 展示。
    private var activeTagBar: some View {
        HStack(spacing: 6) {
            Image(systemName: "tag.fill")
                .font(.system(size: 11))
            #if os(macOS)
            TagScrollRow {
                ForEach(viewModel.activeTags, id: \.self) { tag in
                    TagChip(
                        text: tag,
                        color: Color("appAccent"),
                        font: .system(size: 12, weight: .medium),
                        hPad: 7,
                        vPad: 1,
                        bgOpacity: 0.16
                    )
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            #else
            ForEach(viewModel.activeTags, id: \.self) { tag in
                Text(tag)
                    .font(.system(size: 12, weight: .medium))
                    .padding(.horizontal, 7)
                    .padding(.vertical, 1)
                    .background(Capsule().fill(Color("appAccent").opacity(0.16)))
            }
            Spacer()
            #endif
            Button {
                Task { await viewModel.clearFilter() }
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.system(size: 12))
            }
            .buttonStyle(.plain)
            .help("清除标签筛选")
        }
        .foregroundColor(Color("appAccent"))
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color("appAccent").opacity(0.1))
    }

    private var listPage: some View {
        PageContainer(
            title: "灵阅",
            icon: "doc.richtext",
            // 只有首屏空列表才整页转圈：翻页别把已显示的卡换成 spinner
            isLoading: viewModel.isLoading && viewModel.items.isEmpty,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.items.isEmpty && !viewModel.isLoading,
            emptyMessage: viewModel.keyword.isEmpty && viewModel.activeTags.isEmpty
                ? "暂无内容。可在对话中生成页面后回到此处查看。"
                : "未找到匹配的页面",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.items,
                maxColumns: 3,
                minCardWidth: 260,
                onLoadMore: { await viewModel.loadMore() },
                cardContent: { cardContent(for: $0) }
            )
        }
    }

    #if os(macOS)
    /// 词云：单点标签筛列表、⌘ 多选看交集（与文档页同款，见 docs/design/html-artifact §9.5）。
    private var cloudContent: some View {
        let visible = viewModel.tags.filter { $0.count >= viewModel.minTagCount }
        let neighbors = viewModel.neighbors(of: viewModel.selectedTags)
        return PageContainer(
            title: "标签词云",
            icon: "tag",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: visible.isEmpty && !viewModel.isLoading,
            emptyMessage: viewModel.tags.isEmpty ? "还没有标签" : "没有达到 \(viewModel.minTagCount) 篇的标签",
            onRetry: { Task { await viewModel.load() } }
        ) {
            ZStack(alignment: .bottom) {
                TagCloudView(
                    tags: visible,
                    query: viewModel.cloudQuery,
                    selected: viewModel.selectedTags,
                    neighbors: neighbors,
                    onTap: { tag in Task { await viewModel.selectTag(tag) } },
                    onCmdTap: { tag in viewModel.toggleSelect(tag) },
                    helpLabel: "页"
                )
                VStack(spacing: 6) {
                    if !viewModel.selectedTags.isEmpty {
                        selectionBar
                    }
                    HStack {
                        Spacer()
                        cloudHint
                    }
                }
                .padding(12)
            }
        }
    }

    private var selectionBar: some View {
        HStack(spacing: 8) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 5) {
                    ForEach(viewModel.selectedTags.sorted(), id: \.self) { tag in
                        TagChip(
                            text: tag,
                            color: Color("appAccent"),
                            font: .system(size: 11),
                            hPad: 7,
                            vPad: 2,
                            bgOpacity: 0.14
                        )
                    }
                }
            }
            Button {
                Task { await viewModel.filterBySelection() }
            } label: {
                Text(viewModel.resultCount.map { "查看 \($0) 页" } ?? "查看结果")
                    .font(.system(size: 12, weight: .semibold))
                    .padding(.horizontal, 4)
            }
            .buttonStyle(.borderedProminent)
            .tint(Color("appAccent"))
            .controlSize(.small)
            .fixedSize()
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 7)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5)
        )
    }

    private var cloudHint: some View {
        HStack(spacing: 5) {
            KeycapView(label: "⌘")
            Text("+ 点击 = 多选标签")
            Text("·").opacity(0.4)
            Text("点击 = 筛选")
        }
        .font(.system(size: 10))
        .foregroundColor(.secondary)
    }
    #endif

    private func cardContent(for item: ArtifactSummary) -> some View {
        CardView(config: cardConfig(for: item))
            .cardSelection(
                isSelecting: viewModel.isSelecting,
                isSelected: viewModel.selectedIDs.contains(item.id)
            )
    }

    private func cardConfig(for item: ArtifactSummary) -> CardConfig {
        CardConfig(
            title: item.title.isEmpty ? "未命名页面" : item.title,
            subtitle: item.summary,
            tags: item.tags.tagBadges,
            footerText: String(item.createdAt.prefix(10)),
            style: .document,
            // 选择态让位给勾选圈（两者都占右上角）；删除也随菜单一起藏起来，避免误点
            menuActions: viewModel.isSelecting ? [] : menuActions(for: item),
            onTap: {
                if viewModel.isSelecting {
                    viewModel.toggleSelection(item.id)
                } else {
                    ArtifactOpenCoordinator.shared.open(id: item.id, title: item.title)
                }
            }
        )
    }

    /// 卡片右上角「⋯」：分享 / 撤销分享 / 下载 / 导出 PDF / 删除（见 `docs/design/card-more-menu`）。
    /// 分享与撤销链接两端都有（`copyToPasteboard` 有 iOS 分支）；下载与 PDF 依赖 `NSSavePanel`，仅 macOS。
    private func menuActions(for item: ArtifactSummary) -> [CardAction] {
        var actions: [CardAction] = [
            CardAction(label: "分享链接", icon: "link", role: .normal) {
                Task { await ArtifactOpenCoordinator.shared.copyShareLink(id: item.id) }
            },
            // 详情页右上浮钮撤掉后，这里是撤销分享的唯一入口（见 `artifact-topbar-actions`）。
            // role 用 normal：卡片菜单只在第一颗 destructive（删除）前插分隔线，这里红了会切错位置。
            CardAction(label: "撤销分享", icon: "xmark.circle", role: .normal) {
                Task { await ArtifactOpenCoordinator.shared.revokeShare(id: item.id) }
            },
        ]
        #if os(macOS)
        actions.append(CardAction(label: "下载 HTML", icon: "arrow.down.circle", role: .normal) {
            Task { await ArtifactOpenCoordinator.shared.downloadHTML(id: item.id, title: item.title) }
        })
        // 导出 PDF 暂只给 dev 构建（2026-10-30）：大页面的图片/分页还没在真实产物上验够，
        // 放开只需删掉这个 if（见 docs/design/card-more-menu §9）。
        if LocalPaths.isDev {
            actions.append(CardAction(label: "导出 PDF", icon: "doc.richtext", role: .normal) {
                Task { await ArtifactOpenCoordinator.shared.exportPDF(id: item.id, title: item.title) }
            })
        }
        #endif
        actions.append(CardAction(label: "删除", icon: "trash", role: .destructive) {
            pendingDeletion = item
        })
        return actions
    }
}
