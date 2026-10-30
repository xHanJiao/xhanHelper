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
        PageContainer(
            title: "灵阅",
            icon: "doc.richtext",
            // 只有首屏空列表才整页转圈：翻页别把已显示的卡换成 spinner
            isLoading: viewModel.isLoading && viewModel.items.isEmpty,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.items.isEmpty && !viewModel.isLoading,
            emptyMessage: "暂无内容。可在对话中生成页面后回到此处查看。",
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
        .task { await viewModel.load() }
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

    /// 卡片右上角「⋯」：分享 / 下载 / 导出 PDF / 删除（见 `docs/design/card-more-menu`）。
    /// 分享链接两端都有（`copyToPasteboard` 有 iOS 分支）；下载与 PDF 依赖 `NSSavePanel`，仅 macOS。
    private func menuActions(for item: ArtifactSummary) -> [CardAction] {
        var actions: [CardAction] = [
            CardAction(label: "分享链接", icon: "link", role: .normal) {
                Task { await ArtifactOpenCoordinator.shared.copyShareLink(id: item.id) }
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
