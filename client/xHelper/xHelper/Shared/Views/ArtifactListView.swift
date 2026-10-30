import SwiftUI

/// 右栏「灵阅」：聊天里生成过的 HTML 产物（标题 + 摘要 + 日期），点一条在右栏开 tab。
///
/// 观感与文档列表同款（`PageContainer` + `CardGridView`），但数据源是 artifacts 表、
/// 跟消息无关：会话归档了这里照样能翻（见 docs/design/html-artifact §9.5）。
///
/// 支持多选批量删除（2026-10-29）：改版已改为原地覆盖（§4.2），但此前改十几次样式
/// 堆下的同款页面还得清，逐条删太费手（§9.7）。
struct ArtifactListView: View {
    @ObservedObject var viewModel: ArtifactViewModel

    @State private var selecting = false
    @State private var selected: Set<String> = []
    /// 待确认的删除：单条或批量。共用一个 alert —— 同一视图挂两个 `.alert` 会互相覆盖。
    @State private var pendingDeletion: PendingDeletion?

    private enum PendingDeletion {
        case one(ArtifactSummary)
        case many(Set<String>)
    }

    var body: some View {
        PageContainer(
            title: "灵阅",
            icon: "doc.richtext",
            // 只有首屏空列表才整页转圈：翻页别把已显示的卡换成 spinner
            isLoading: viewModel.isLoading && viewModel.items.isEmpty,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.items.isEmpty && !viewModel.isLoading,
            emptyMessage: "灵阅还是空的——在聊天里让我做一张手账或对比图试试",
            onRetry: { Task { await viewModel.load() } }
        ) {
            VStack(spacing: 0) {
                toolbar
                CardGridView(
                    items: viewModel.items,
                    maxColumns: 3,
                    minCardWidth: 260,
                    onLoadMore: { await viewModel.loadMore() },
                    cardContent: { cardContent(for: $0) }
                )
            }
        }
        .task {
            await viewModel.load()
            // 重载后可能有条目已被别处删掉：勾选里不能留幽灵 id
            selected = selected.intersection(Set(viewModel.items.map(\.id)))
        }
        .alert(
            "删除页面？",
            isPresented: Binding(get: { pendingDeletion != nil }, set: { if !$0 { pendingDeletion = nil } }),
            presenting: pendingDeletion
        ) { pending in
            Button("删除", role: .destructive) { perform(pending) }
            Button("取消", role: .cancel) {}
        } message: { pending in
            Text(deleteMessage(pending))
        }
    }

    // MARK: - 多选

    private var toolbar: some View {
        HStack(spacing: 12) {
            if selecting {
                Button(selected.count == viewModel.items.count ? "取消全选" : "全选") { toggleAll() }
                    .foregroundColor(Color("appAccent"))
                Text("已选 \(selected.count)")
                    .foregroundColor(.secondary)
                Spacer()
                Button("删除所选", role: .destructive) {
                    guard !selected.isEmpty else { return }
                    pendingDeletion = .many(selected)
                }
                .disabled(selected.isEmpty)
                Button("取消") { exitSelection() }
                    .foregroundColor(Color("appAccent"))
            } else {
                Spacer()
                Button("选择") {
                    selecting = true
                    selected.removeAll()
                }
                .foregroundColor(Color("appAccent"))
            }
        }
        .font(.system(size: 12))
        .buttonStyle(.plain)
        .padding(.horizontal, 16)
        .padding(.top, 8)
    }

    @ViewBuilder
    private func cardContent(for item: ArtifactSummary) -> some View {
        ZStack(alignment: .topTrailing) {
            CardView(config: cardConfig(for: item))
            if selecting {
                Image(systemName: selected.contains(item.id) ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 17))
                    .foregroundColor(selected.contains(item.id) ? Color("appAccent") : .secondary)
                    .padding(5)
                    .background(.thinMaterial, in: Circle())
                    .padding(10)
                    // 整张卡已经是切换命中区；圈只是指示器，别让它截走角落的点击
                    .allowsHitTesting(false)
                    .accessibilityLabel(selected.contains(item.id) ? "已选中" : "未选中")
                    .accessibilityAddTraits(selected.contains(item.id) ? .isSelected : [])
            }
        }
    }

    private func cardConfig(for item: ArtifactSummary) -> CardConfig {
        CardConfig(
            title: item.title.isEmpty ? "未命名页面" : item.title,
            subtitle: item.summary,
            footerText: String(item.createdAt.prefix(10)),
            style: .document,
            // 选择态里隐藏单条删除，避免误点到「删这一张」的弹窗
            actions: selecting ? [] : [
                CardAction(label: "删除", icon: "trash", role: .destructive) {
                    pendingDeletion = .one(item)
                }
            ],
            onTap: {
                if selecting {
                    toggle(item.id)
                } else {
                    ArtifactOpenCoordinator.shared.open(id: item.id, title: item.title)
                }
            }
        )
    }

    private func toggle(_ id: String) {
        if selected.contains(id) {
            selected.remove(id)
        } else {
            selected.insert(id)
        }
    }

    private func toggleAll() {
        if selected.count == viewModel.items.count {
            selected.removeAll()
        } else {
            selected = Set(viewModel.items.map(\.id))
        }
    }

    private func exitSelection() {
        selecting = false
        selected.removeAll()
    }

    // MARK: - 删除

    private func perform(_ pending: PendingDeletion) {
        switch pending {
        case .one(let item):
            Task { await viewModel.delete(id: item.id) }
        case .many(let ids):
            Task {
                // 失败时留在选择态，用户可以直接再点一次（toast 已说明原因）
                if await viewModel.deleteMany(ids: Array(ids)) {
                    exitSelection()
                } else {
                    // 部分成功的分块已经删掉了：从勾选里去掉，别让「已选 N」虚高、
                    // 也别再把幽灵 id 重发一遍
                    selected = selected.intersection(Set(viewModel.items.map(\.id)))
                }
            }
        }
    }

    private func deleteMessage(_ pending: PendingDeletion) -> String {
        switch pending {
        case .one(let item):
            return "《\(item.title.isEmpty ? "未命名页面" : item.title)》将被删除，不能恢复；已分享的链接会立即失效。"
        case .many(let ids):
            return "选中的 \(ids.count) 页将被删除，不能恢复；已分享的链接会立即失效。"
        }
    }
}
