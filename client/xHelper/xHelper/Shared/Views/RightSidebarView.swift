import SwiftUI
import WebKit
#if os(macOS)
import AppKit
#endif

/// 右侧栏：浏览面板（默认态，无 tab 条）+ 浏览器标签页（tab 条 + 返回按钮）。
struct RightSidebarView: View {
    @ObservedObject var manager: RightSidebarManager
    @ObservedObject var billVM: BillViewModel
    @ObservedObject var memoryVM: MemoryViewModel
    @ObservedObject var noteVM: NoteViewModel
    @ObservedObject var documentVM: DocumentViewModel
    @ObservedObject var themeVM: ThemeViewModel
    let baseURL: String
    let token: String

    @State private var isHovered = false
    @ObservedObject private var downloadStore = DownloadHistoryStore.shared

    #if os(macOS)
    @ObservedObject private var faviconStore = FaviconStore.shared
    #endif

    var body: some View {
        VStack(spacing: 0) {
            if !manager.isBrowseActive {
                browserTabBar
                Divider()
            }
            contentArea
        }
        .frame(minWidth: Layout.rightSidebarMinWidth, idealWidth: manager.width, maxWidth: .infinity)
        .modifier(CardBackgroundModifier(isLifted: isHovered))
        .onHover { isHovered = $0 }
    }

    // MARK: - 浏览器 tab 栏（仅浏览器态显示）

    /// 退出浏览器确认（关闭全部标签，释放网页内存；sheet 确认，非阻塞）
    private func confirmExitBrowser() {
        #if os(macOS)
        let alert = NSAlert()
        alert.messageText = "退出浏览器"
        alert.informativeText = "将关闭所有打开的标签页并退出浏览器，重新进入时将是一个全新的空浏览状态。"
        alert.addButton(withTitle: "退出")
        alert.addButton(withTitle: "取消")
        let exit: () -> Void = { BrowserToolProvider.shared.exitBrowser() }
        guard let window = NSApp.keyWindow ?? NSApp.windows.first else {
            DispatchQueue.main.async {
                guard alert.runModal() == .alertFirstButtonReturn else { return }
                exit()
            }
            return
        }
        alert.beginSheetModal(for: window) { response in
            guard response == .alertFirstButtonReturn else { return }
            exit()
        }
        #else
        BrowserToolProvider.shared.exitBrowser()
        #endif
    }

    private var browserTabBar: some View {
        HStack(spacing: 0) {
            // 返回浏览面板入口
            Button {
                manager.showBrowsePanel()
            } label: {
                HStack(spacing: 3) {
                    Image(systemName: "chevron.left").font(.system(size: 10, weight: .semibold))
                    Text("返回").font(.system(size: 11))
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 6)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("返回浏览面板")

            // 退出浏览器：关闭所有标签、彻底退出（区别于「返回」保留标签）。确认后执行。
            Button {
                confirmExitBrowser()
            } label: {
                HStack(spacing: 3) {
                    Image(systemName: "rectangle.portrait.and.arrow.right")
                        .font(.system(size: 10, weight: .semibold))
                    Text("退出").font(.system(size: 11))
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 6)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("退出浏览器，关闭所有标签")

            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 0) {
                    ForEach(manager.tabs) { tab in
                        tabButton(tab)
                    }
                }
                .frame(height: 32)
            }
            // 原固定按钮（PiP/清数据/+/下载/⭐/书签/🕐）已移到顶栏中槽位（TopBarRegistry）
        }
        .padding(.leading, 6)
    }

    /// tab 按钮：网页 tab 可拖拽重排；特殊 tab（下载/收藏/历史）固定不可拖
    @ViewBuilder
    private func tabButton(_ tab: RightSidebarTab) -> some View {
        if RightSidebarManager.specialTabIDs.contains(tab.id) {
            browserTabButton(tab)
        } else {
            browserTabButton(tab)
                .draggable(tab.id)
                .dropDestination(for: String.self) { items, _ in
                    guard let dragged = items.first else { return false }
                    manager.moveTab(from: dragged, to: tab.id)
                    return true
                }
        }
    }

    private func browserTabButton(_ tab: RightSidebarTab) -> some View {
        let isActive = manager.activeTabId == tab.id
        // 下载 tab 禁关态：它是唯一 tab 且仍有下载（浏览器保活 A）
        let isDownloadTab = tab.id == RightSidebarManager.downloadTabID
        let hasOtherTabs = manager.tabs.contains { $0.id != RightSidebarManager.downloadTabID }
        let downloadLocked = isDownloadTab && downloadStore.hasActiveDownloads && !hasOtherTabs
        return HStack(spacing: 4) {
            faviconView(tab)
            Text(tab.title)
                .font(.system(size: 11))
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: 160)
            Button { BrowserToolProvider.shared.closeTab(tab.id) } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 8, weight: .bold))
                    .opacity(downloadLocked ? 0.35 : 1)
            }
            .buttonStyle(.plain)
            .help(downloadLocked ? "下载进行中，完成后可关闭" : "关闭")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(
            isActive
                ? AnyShapeStyle(.regularMaterial)
                : AnyShapeStyle(.clear),
            in: UnevenRoundedRectangle(
                topLeadingRadius: 6, bottomLeadingRadius: 0,
                bottomTrailingRadius: 0, topTrailingRadius: 6
            )
        )
        .contentShape(Rectangle())
        .onTapGesture {
            manager.activeTabId = tab.id
            BrowserToolProvider.shared.selectTab(tab.id)
        }
    }

    /// tab 图标：优先显示网站 favicon，否则回退系统符号
    @ViewBuilder
    private func faviconView(_ tab: RightSidebarTab) -> some View {
        #if os(macOS)
        if let host = tab.host, let img = faviconStore.image(forHost: host) {
            Image(nsImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 12, height: 12)
        } else {
            Image(systemName: tab.icon).font(.system(size: 11))
        }
        #else
        Image(systemName: tab.icon).font(.system(size: 11))
        #endif
    }

    // MARK: - Content

    @ViewBuilder
    private var contentArea: some View {
        if manager.isBrowseActive {
            BrowsePanelView(
                manager: manager,
                billVM: billVM,
                memoryVM: memoryVM,
                noteVM: noteVM,
                documentVM: documentVM,
                themeVM: themeVM,
                baseURL: baseURL,
                token: token
            )
        } else if let active = manager.tabs.first(where: { $0.id == manager.activeTabId }) {
            // 节能：只渲染激活 tab。非激活 WKWebView 摘除视图层级（不再合成渲染），
            // 实例保留在 BrowserToolProvider.tabs，切回时同 NSView 重新挂载、页面状态不丢。
            // .id() 必须：content 是同类型 AnyView(BrowserTabView)，不加 id 切 tab 时
            // SwiftUI 会判定"同位置同类型→原地更新"，旧 webView 不卸、新 webView 不挂。
            active.content.id(active.id)
        }
    }
}
