import SwiftUI

/// 文档详情 tab 的内容：本篇快照 + 跟随 `DocumentViewModel` 的重载。
///
/// 统一 tab 之后一个 tab 只认一篇文档（原来靠 `openDocument` 单值切页）。
/// 重载仍走 VM 的 `reloadOpenDocument()`，所以**本 tab 处于激活态时**才把 `openDocument` 指过来。
/// 细节 tab 会被保活（opacity 0 留在视图层级），所以不能靠 onAppear 判断激活，得看 manager。
struct DocumentTabView: View {
    @ObservedObject var viewModel: DocumentViewModel
    @ObservedObject var manager: RightSidebarManager
    let onBack: () -> Void
    @State private var document: Document

    private var tabID: String { RightSidebarManager.documentTabID(document.id) }

    init(document: Document, viewModel: DocumentViewModel, manager: RightSidebarManager, onBack: @escaping () -> Void) {
        _document = State(initialValue: document)
        self.viewModel = viewModel
        self.manager = manager
        self.onBack = onBack
    }

    var body: some View {
        DocumentFullView(
            document: document,
            apiClient: viewModel.apiClient,
            viewModel: viewModel,
            onBack: onBack
        )
        // 换篇重建：滚动位置 / 目录展开态归零（查找栏是 @AppStorage，跨篇保留）
        .id(document.id)
        .onChange(of: manager.activeTabId, initial: true) { _, active in
            syncActivation(active == tabID)
        }
        .onDisappear {
            syncActivation(false)
        }
        .onChange(of: viewModel.openDocument) { _, new in
            guard let new, new.id == document.id else { return }
            document = new
        }
    }

    private func syncActivation(_ active: Bool) {
        if active {
            viewModel.openDocument = document
            DocumentSelectionCoordinator.shared.select(document)
        } else {
            if viewModel.openDocument?.id == document.id { viewModel.openDocument = nil }
            if DocumentSelectionCoordinator.shared.selectedDocument?.id == document.id {
                DocumentSelectionCoordinator.shared.deselect()
            }
        }
    }
}
