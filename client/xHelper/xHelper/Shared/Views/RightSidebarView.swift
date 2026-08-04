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
    @ObservedObject private var bookmarkStore = BookmarkStore.shared

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
        .frame(width: max(manager.width, Layout.rightSidebarMinWidth))
        .modifier(CardBackgroundModifier(isLifted: isHovered))
        .onHover { isHovered = $0 }
    }

    // MARK: - Browser tab bar（仅浏览器态显示）

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

            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 0) {
                    ForEach(manager.tabs) { tab in
                        tabButton(tab)
                    }
                }
                .frame(height: 32)
            }

            // 聊天画中画切换：释放中间聊天区，聊天缩成悬浮面板
            Button {
                manager.isPipChat.toggle()
                pipLog("toggle button: isPipChat=\(manager.isPipChat) tabs=\(manager.tabs.count) activeTab=\(manager.activeTabId ?? "nil")")
            } label: {
                Image(systemName: manager.isPipChat ? "pip.exit" : "pip.enter")
                    .font(.system(size: 11, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .foregroundColor(manager.isPipChat ? Color("appAccent") : .secondary)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(manager.isPipChat ? "退出画中画" : "画中画：聊天悬浮在浏览器上")
            .padding(.horizontal, 2)

            // S5：清除浏览数据（Cookie/登录态/缓存），一键退出所有登录态
            Button {
                confirmClearBrowsingData()
            } label: {
                Image(systemName: "trash")
                    .font(.system(size: 11, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("清除浏览数据（Cookie/登录态/缓存）")
            .padding(.horizontal, 2)

            // 新空白页按钮（固定在 tab 条最右侧，不随 tab 滚动）
            Button {
                BrowserToolProvider.shared.openBlankTab()
            } label: {
                Image(systemName: "plus")
                    .font(.system(size: 11, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("新空白页")
            .padding(.horizontal, 4)

            // 下载记录入口（同 Chrome 下箭头）
            Button {
                manager.showDownloads()
            } label: {
                Image(systemName: "arrow.down.circle")
                    .font(.system(size: 12, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("下载记录")
            .padding(.trailing, 2)

            // 收藏当前页（实心=已收藏）
            Button {
                toggleBookmarkCurrentPage()
            } label: {
                Image(systemName: currentPageBookmarked ? "star.fill" : "star")
                    .font(.system(size: 11, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(currentPageBookmarked ? .yellow : .secondary)
            .help(currentPageBookmarked ? "取消收藏当前页" : "收藏当前页")
            .padding(.trailing, 2)

            // 收藏夹面板入口
            Button {
                manager.openBookmarkTab()
            } label: {
                Image(systemName: "bookmark")
                    .font(.system(size: 12, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("收藏夹")
            .padding(.trailing, 2)

            // 历史面板入口
            Button {
                manager.openHistoryTab()
            } label: {
                Image(systemName: "clock")
                    .font(.system(size: 12, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("浏览历史")
            .padding(.trailing, 4)
        }
        .padding(.leading, 6)
    }

    /// 当前页是否已收藏
    private var currentPageBookmarked: Bool {
        guard let url = BrowserToolProvider.shared.activeWebView?.url?.absoluteString else { return false }
        return bookmarkStore.isBookmarked(url: url)
    }

    private func toggleBookmarkCurrentPage() {
        guard let wv = BrowserToolProvider.shared.activeWebView,
              let url = wv.url,
              let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https" else { return }
        let title = wv.title ?? url.host ?? ""
        bookmarkStore.toggle(title: title, url: url.absoluteString)
    }

    /// S5：确认后清除全部网站数据（cookie/localStorage/site data/缓存）。
    /// 用非阻塞 sheet（runModal 会卡主线程，与 JS 弹窗同样的终止死锁隐患）。
    private func confirmClearBrowsingData() {
        #if os(macOS)
        let alert = NSAlert()
        alert.messageText = "清除浏览数据"
        alert.informativeText = "将删除所有网站的 Cookie、本地存储与缓存，退出所有登录态。此操作不可撤销。"
        alert.addButton(withTitle: "清除")
        alert.addButton(withTitle: "取消")

        let clearData: () -> Void = {
            WKWebsiteDataStore.default().removeData(
                ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(),
                modifiedSince: .distantPast
            ) {
                ToastCenter.shared.show("浏览数据已清除")
            }
        }

        guard let window = NSApp.keyWindow else {
            DispatchQueue.main.async {
                guard alert.runModal() == .alertFirstButtonReturn else { return }
                clearData()
            }
            return
        }
        alert.beginSheetModal(for: window) { response in
            guard response == .alertFirstButtonReturn else { return }
            clearData()
        }
        #endif
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
