#if os(macOS)
import SwiftUI

/// 灵阅详情（右栏 tab 的内容）：左侧「灵阅列表」换篇栏 + 页面区。
///
/// 换篇语义 = 打开 / 切到对应 tab（见 docs/design/artifact-nav-sidebar AN-2）：
/// 点行走 `ArtifactOpenCoordinator.open`，不替换本 tab。
///
/// 内存约定沿用 `HTMLArtifactView`：内容由 coordinator 塞进 `RightSidebarTab.content`，
/// 切走即销毁、切回来重新加载；别把 WKWebView 实例存在这里。
struct ArtifactDetailView: View {
    let id: String
    let title: String
    /// nil = 正文还在路上：页面区显示骨架，但侧栏已可用（能连续点下一篇，别等几秒的静默）
    let html: String?
    /// 与浏览面板共用的灵阅列表（`ContentView` 注入 coordinator 的那份）
    @ObservedObject var viewModel: ArtifactViewModel

    @AppStorage("xhan_artifact_nav_collapsed") private var navCollapsed = false
    /// 详情视图自身宽（`onGeometryChange` 写入），窄过硬地板自动收侧栏
    @State private var width: CGFloat = 0

    var body: some View {
        HStack(spacing: 0) {
            if navExpanded {
                navSidebar
            } else {
                SidebarCollapsedStrip(help: "展开灵阅列表") {
                    withAnimation { navCollapsed = false }
                }
                .background(.regularMaterial)
            }
            Divider()
            page
        }
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .task {
            // 从聊天卡片直接打开时面板可能从没进过：列表得自己拉一次。
            // 已有数据不重拉 —— 别把面板翻到的页重置回第一页。
            guard viewModel.items.isEmpty, !viewModel.isLoading else { return }
            await viewModel.load()
        }
    }

    /// 页面列独占安全区：沿用 `HTMLArtifactView` 原来的行为，别把侧栏也拽出去
    /// （侧栏上方是 tab 条，多占一步就可能被压住）。
    @ViewBuilder
    private var page: some View {
        if let html {
            HTMLArtifactView(html: html)
                .ignoresSafeArea()
        } else {
            ArtifactLoadingView(title: title)
                .ignoresSafeArea()
        }
    }

    /// 首次布局前按「很宽」算，避免开场闪一下收窄（与 `DocumentFullView.tocExpanded` 同口径）。
    private var navExpanded: Bool {
        !navCollapsed && (width <= 0 || width >= Layout.artifactNavHardFloor)
    }

    // MARK: - 侧栏

    private var navSidebar: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 4) {
                Text(viewModel.total > 0 ? "灵阅列表 · \(viewModel.total) 篇" : "灵阅列表")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                Spacer(minLength: 4)
                Button {
                    withAnimation { navCollapsed = true }
                } label: {
                    Image(systemName: "chevron.left")
                        .font(.caption.weight(.semibold))
                        .foregroundColor(.secondary)
                        .frame(width: 24, height: 24)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("收起灵阅列表")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)

            Divider().opacity(0.3)

            ArtifactNavList(
                currentID: id,
                currentTitle: title,
                items: viewModel.items,
                isLoading: viewModel.isLoading,
                hasError: viewModel.errorMessage != nil,
                onSelect: { ArtifactOpenCoordinator.shared.open(id: $0.id, title: $0.title) },
                onSelectCurrent: { ArtifactOpenCoordinator.shared.open(id: id, title: title) },
                onLoadMore: { Task { await viewModel.loadMore() } }
            )
        }
        .frame(width: Layout.docTocExpandedWidth)
        // 纸底与文档侧栏对齐（右栏本身没有卡片底，不铺就直接压在照片底图上）；
        // 材质而非 Color.white：深色模式下 .primary 文字才读得出来。
        .background(.regularMaterial)
    }
}

/// 侧栏列表体：纯展示（数据由 `ArtifactDetailView` 传，便于 `#Preview` 各状态）。
private struct ArtifactNavList: View {
    let currentID: String
    let currentTitle: String
    let items: [ArtifactSummary]
    let isLoading: Bool
    let hasError: Bool
    let onSelect: (ArtifactSummary) -> Void
    /// 钉行（当前页）点击：切回本 tab，与点当前页那一行等价（幂等）
    let onSelectCurrent: () -> Void
    let onLoadMore: () -> Void

    @State private var hoveredID: String?
    /// 本次进入是否已经自动定位过（只定位一次，别在续拉 / 刷新时抢用户的手动滚动）
    @State private var didAutoScroll = false

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    if !currentInList {
                        // 当前页不在已加载的列表里（从聊天打开 / 翻在第 2 页往后）：顶部钉一行标出。
                        // 只钉不插回原位，与文档侧栏 SM-5 同口径。
                        SidebarNavRow(
                            title: displayTitle(currentTitle),
                            subtitle: "当前页面",
                            isCurrent: true,
                            isHovered: false,
                            action: onSelectCurrent,
                            onHover: { _ in }
                        )
                        Divider().opacity(0.3).padding(.vertical, 2)
                    }
                    ForEach(items, id: \.id) { item in
                        SidebarNavRow(
                            title: displayTitle(item.title),
                            subtitle: navSubtitle(item),
                            isCurrent: item.id == currentID,
                            isHovered: hoveredID == item.id,
                            action: { onSelect(item) },
                            onHover: { inside in
                                if inside {
                                    hoveredID = item.id
                                } else if hoveredID == item.id {
                                    hoveredID = nil
                                }
                            }
                        )
                        .id(item.id)
                        .onAppear {
                            if item.id == items.last?.id { onLoadMore() }
                        }
                    }
                    if items.isEmpty {
                        Text(isLoading ? "正在加载…" : (hasError ? "列表加载失败" : "暂无内容"))
                            .font(.caption)
                            .foregroundColor(.secondary)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 8)
                    }
                }
                .padding(.horizontal, 6)
                .padding(.vertical, 6)
            }
            .onAppear { autoScroll(to: proxy) }
            // 从聊天卡片首次打开时，onAppear 那一刻列表还是空的（`.task` 里的 load 还没回来）。
            // 只认「0 → N」这一次：等首次加载完成后补一次定位；续拉 / 面板刷新别抢用户的手动滚动。
            .onChange(of: items.count) { old, new in
                guard old == 0, new > 0 else { return }
                autoScroll(to: proxy)
            }
        }
    }

    /// 把当前项放到视口上方约 12%（上面留两三行上下文），本次进入只做一次。
    private func autoScroll(to proxy: ScrollViewProxy) {
        guard !didAutoScroll, currentInList else { return }
        didAutoScroll = true
        // 刚插入的行可能还没布局（首次 load 完成就在同一个更新里），延后一个 runloop 再滚；
        // shortcut: LazyVStack 对未物化行 scrollTo 仍可能静默不生效，退化方案是顶部钉行（未预置）
        DispatchQueue.main.async {
            withAnimation(.easeInOut(duration: 0.2)) {
                proxy.scrollTo(currentID, anchor: UnitPoint(x: 0, y: 0.12))
            }
        }
    }

    private var currentInList: Bool {
        items.contains { $0.id == currentID }
    }

    private func displayTitle(_ text: String) -> String {
        text.isEmpty ? "未命名页面" : text
    }

    /// 行次信息：「相对时间 · 摘要」（摘要压成一行，太长的由 `lineLimit(1)` 截）。
    private func navSubtitle(_ item: ArtifactSummary) -> String {
        var parts = [formatConversationDate(item.createdAt)]
        let summary = item.summary
            .components(separatedBy: .whitespacesAndNewlines)
            .filter { !$0.isEmpty }
            .joined(separator: " ")
        if !summary.isEmpty { parts.append(summary) }
        return parts.joined(separator: " · ")
    }
}

#Preview("灵阅列表") {
    ArtifactNavList(
        currentID: "2",
        currentTitle: "当前页",
        items: [
            ArtifactSummary(id: "1", title: "今天的手账", summary: "这一天做了什么、花了多少",
                            conversationId: "c1", createdAt: "2026-10-30T21:30:00+08:00"),
            ArtifactSummary(id: "2", title: "产品对比报告", summary: "三款产品的价格、续航、手感",
                            conversationId: "c1", createdAt: "2026-10-30T20:00:00+08:00"),
        ],
        isLoading: false,
        hasError: false,
        onSelect: { _ in },
        onSelectCurrent: {},
        onLoadMore: {}
    )
    .frame(width: Layout.docTocExpandedWidth, height: 420)
    .background(Color.white)
}
#endif
