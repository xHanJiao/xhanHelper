import SwiftUI
import Combine

/// 列表/工具 tab 的顶栏动作（原来散在各面板页头里）。
///
/// 面板内部的可变状态（搜索框、二次确认、回收站批量）留在内容区，这里只放「跨面板同义」的动作：
/// 刷新、多选、导入、导出。具体执行走通知回到 `PanelTabView`，避免把 UI 状态搬进顶栏注册表。
enum SidebarPanelAction: String {
    case refresh
    case multiselect
    case exportCSV
    case importBook
}

struct SidebarPanelActionEvent {
    let panel: BrowsePanel
    let action: SidebarPanelAction
}

extension Notification.Name {
    static let sidebarPanelAction = Notification.Name("xhan.sidebarPanelAction")
}

enum RightSidebarTabActions {
    /// 注册「列表 tab 动作」组（priority 7：低于浏览器 10 / 文档详情 8 / 灵阅详情 8）。
    @MainActor
    static func register(
        manager: RightSidebarManager,
        documentVM: DocumentViewModel,
        artifactVM: ArtifactViewModel
    ) {
        func post(_ action: SidebarPanelAction) {
            guard let panel = manager.activePanel else { return }
            NotificationCenter.default.post(
                name: .sidebarPanelAction,
                object: SidebarPanelActionEvent(panel: panel, action: action)
            )
        }

        var buttons: [TopBarButton] = [
            TopBarButton(id: "tab-refresh",
                             icon: { "arrow.clockwise" },
                             enabled: { true },
                             action: { post(.refresh) },
                             help: { "刷新" },
                             isRefresh: true),
                TopBarButton(id: "tab-multiselect",
                             icon: { "checkmark.circle" },
                             enabled: { true },
                             action: { post(.multiselect) },
                             help: { "多选管理" },
                             visible: {
                                 if manager.activePanel == .documents {
                                     return documentVM.mode == .list
                                 }
                                 if manager.activePanel == .artifacts {
                                     return artifactVM.mode == .list
                                 }
                                 return manager.activePanel == .images
                             }),
            TopBarButton(id: "tab-export-csv",
                         icon: { "tray.and.arrow.up" },
                         enabled: { true },
                         action: { post(.exportCSV) },
                         help: { "导出 CSV" },
                         visible: { manager.activePanel == .bills }),
        ]
        #if os(macOS)
        buttons.insert(TopBarButton(id: "tab-import-book",
                                    icon: { "tray.and.arrow.down" },
                                    enabled: { true },
                                    action: { post(.importBook) },
                                    help: { "导入 EPUB" },
                                    visible: { manager.activePanel == .books }),
                       at: buttons.count - 1)
        #endif

        TopBarRegistry.shared.register(TopBarGroup(
            id: "tab-actions",
            slot: .center,
            priority: 7,
            buttons: buttons,
            visible: { manager.isOpen && manager.activePanel != nil },
            autoOverflow: true
        ))
    }
}
