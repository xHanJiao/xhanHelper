import Foundation
import Combine
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

/// 文档选中状态协调器：追踪当前选中的文档（DocumentFullView 正在展示）。
/// 顶栏文档按钮组按 TopBarRegistry 通用契约声明式显隐：
/// 注册一次 + visible 闭包判「右栏正显示文档详情」+ 选择变化调 refresh()。
/// 右栏状态（browsePanelSelection / activeTabId / isOpen）由 TopBarView 观察回自动重算。
@MainActor
final class DocumentSelectionCoordinator: ObservableObject {
    static let shared = DocumentSelectionCoordinator()

    /// 右侧栏管理器（弱引用，由 ContentView 注入），用于判断文档页是否恰好在右栏前台。
    weak var rightSidebar: RightSidebarManager?

    @Published private(set) var selectedDocument: Document?

    private var groupRegistered = false

    private init() {}

    func select(_ document: Document) {
        selectedDocument = document
        TopBarRegistry.shared.refresh()
    }

    func deselect() {
        selectedDocument = nil
        TopBarRegistry.shared.refresh()
    }

    /// 注册顶栏文档组（幂等，参照浏览器组「注册一次 + visible 闭包」模式）。
    func ensureRegisteredGroup() {
        guard !groupRegistered else { return }
        groupRegistered = true
        TopBarRegistry.shared.register(TopBarGroup(
            id: "document-actions",
            slot: .left,
            priority: 8,
            buttons: [
                TopBarButton(id: "document-download", icon: { "arrow.down.circle" },
                             enabled: { true },
                             action: { [weak self] in self?.downloadSelected() },
                             help: { "下载文档 (.md)" }),
            ],
            visible: { [weak self] in self?.isDocumentDetailVisible ?? false }
        ))
    }

    /// 当前是否应显示文档按钮组：右栏在前台展示文档详情页。
    private var isDocumentDetailVisible: Bool {
        guard let rs = rightSidebar, selectedDocument != nil else { return false }
        // 文档面板选中 + 网页态未激活（无浏览器 tab 抢占）+ 右栏展开
        return rs.isOpen && rs.activeTabId == nil && rs.browsePanelSelection == .documents
    }

    /// 下载当前文档为 .md 文件（macOS NSSavePanel，与账单 CSV 导出同模式）
    private func downloadSelected() {
        guard let doc = selectedDocument else { return }
        #if os(macOS)
        let fileName = (doc.title.isEmpty ? doc.id : doc.title) + ".md"
        let panel = NSSavePanel()
        panel.title = "下载文档"
        panel.nameFieldStringValue = fileName
        panel.allowedContentTypes = [UTType(importedAs: "public.markdown")]
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            do {
                try doc.content.write(to: url, atomically: true, encoding: .utf8)
                ToastCenter.shared.show("已下载 \(url.lastPathComponent)")
            } catch {
                LogBuffer.shared.error("document download failed: \(error.localizedDescription)")
            }
        }
        #endif
    }
}