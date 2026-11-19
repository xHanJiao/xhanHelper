import SwiftUI
#if os(macOS)
import AppKit

/// favicon 只在 macOS 有；iOS 上这一栏恒为 nil（右栏是 macOS 独有）
typealias SidebarFavicon = NSImage
#else
typealias SidebarFavicon = AnyObject
#endif

/// 右栏顶部统一的胶囊 tab 条：首页 chip + 各 tab。
/// 列表/详情/网页/工具页在这里只有"一个 tab"这一种身份（见 docs/design/right-sidebar-unified-tabs）。
struct RightSidebarTabStrip: View {
    @ObservedObject var manager: RightSidebarManager

    #if os(macOS)
    @ObservedObject private var faviconStore = FaviconStore.shared
    #endif
    @ObservedObject private var downloadStore = DownloadHistoryStore.shared

    var body: some View {
        HStack(spacing: 6) {
            homeChip
            if manager.hasTabs {
                closeAllChip
            }

            ScrollViewReader { proxy in
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        ForEach(manager.tabs.filter { $0.kind != .home }) { tab in
                            chip(tab)
                                .id(tab.id)
                        }
                    }
                    .padding(.vertical, 2)
                }
                // ⌃N / ⌘⇧[ ] 跳到屏幕外的 tab 时把它带进视野（已在视野内则不动）
                .onChange(of: manager.activeTabId) { _, id in
                    guard let id,
                          manager.tabs.contains(where: { $0.id == id && $0.kind != .home })
                    else { return }
                    withAnimation(.easeInOut(duration: 0.15)) { proxy.scrollTo(id) }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 12)
        .padding(.top, 2)
        .padding(.bottom, 8)
    }

    // MARK: - 首页

    private var homeChip: some View {
        let active = manager.isBrowseActive
        return Button {
            manager.goHome()
        } label: {
            Image(systemName: "house")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(active ? Color("appAccent") : .secondary)
                .frame(width: 26, height: 26)
                .background(chipBackground(active: active), in: Capsule())
                .overlay(Capsule().stroke(chipStroke(active: active), lineWidth: 0.5))
        }
        .buttonStyle(.plain)
        .help("首页")
    }

    // MARK: - 单个 tab

    /// 全部关闭（网页/文档/列表都关）后回首页；没有 tab 时整颗不显示
    private var closeAllChip: some View {
        Button {
            manager.closeAllTabs()
        } label: {
            // 纯叉不套圈（xmark.circle 在圆底胶囊里 = 同心双圈，像"关闭这个圆"）；
            // 叉本身笔画少，字号要比邻居图标大一档才不显小
            Image(systemName: "xmark")
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(.secondary)
                .frame(width: 26, height: 26)
                .background(chipBackground(active: false), in: Capsule())
                .overlay(Capsule().stroke(chipStroke(active: false), lineWidth: 0.5))
        }
        .buttonStyle(.plain)
        .help("全部关闭，回到首页")
    }

    private func chip(_ tab: RightSidebarTab) -> some View {
        TabChipView(
            tab: tab,
            active: manager.activeTabId == tab.id,
            favicon: favicon(for: tab),
            canClose: !isDownloadLocked(tab),
            onSelect: {
                manager.activateTab(id: tab.id)
                #if os(macOS)
                // 网页 tab：激活时同步引擎焦点（activateTab 内部已做，这里不重复）
                #endif
            },
            onClose: { close(tab) }
        )
        .draggable(tab.id)
        .dropDestination(for: String.self) { items, _ in
            guard let dragged = items.first else { return false }
            manager.moveTab(from: dragged, to: tab.id)
            return true
        }
        .contextMenu { menu(tab) }
    }

    private func favicon(for tab: RightSidebarTab) -> SidebarFavicon? {
        #if os(macOS)
        guard let host = tab.host else { return nil }
        return faviconStore.image(forHost: host)
        #else
        return nil
        #endif
    }

    private func isDownloadLocked(_ tab: RightSidebarTab) -> Bool {
        tab.id == RightSidebarManager.downloadTabID
            && downloadStore.hasActiveDownloads
            && !manager.tabs.contains { $0.kind != .home && $0.id != RightSidebarManager.downloadTabID }
    }

    private func close(_ tab: RightSidebarTab) {
        switch tab.kind {
        case .web, .artifact, .tool:
            // 走 provider：网页要 teardown session、灵阅/工具页要记恢复栈
            BrowserToolProvider.shared.closeTab(tab.id)
        default:
            manager.closeTab(tab.id)
        }
    }

    @ViewBuilder
    private func menu(_ tab: RightSidebarTab) -> some View {
        if tab.kind == .web {
            Button("重新加载") { BrowserToolProvider.shared.reloadTab(tab.id) }
            Button("移到右窗格") { manager.moveTabToRight(id: tab.id) }
            Divider()
        }
        Button("关闭") { close(tab) }
            .disabled(isDownloadLocked(tab))
        Button("关闭其他标签页") {
            for other in manager.tabs where other.id != tab.id && other.kind != .home {
                close(other)
            }
        }
        Button("关闭右侧标签页") {
            let ids = manager.tabs.map(\.id)
            guard let i = ids.firstIndex(of: tab.id) else { return }
            for other in manager.tabs.dropFirst(i + 1) where other.kind != .home {
                close(other)
            }
        }
        if manager.tabs.contains(where: { $0.kind == .web }) {
            Divider()
            Button("退出浏览器（关闭全部标签）") {
                BrowserToolProvider.shared.exitBrowser()
            }
        }
    }

    private func chipBackground(active: Bool) -> Color {
        active ? Color.primary.opacity(0.14) : Color.primary.opacity(0.05)
    }

    private func chipStroke(active: Bool) -> Color {
        Color.white.opacity(active ? 0.28 : 0.16)
    }
}

/// 单枚 tab chip：悬停出 ×，激活高亮。
private struct TabChipView: View {
    let tab: RightSidebarTab
    let active: Bool
    let favicon: SidebarFavicon?
    let canClose: Bool
    let onSelect: () -> Void
    let onClose: () -> Void

    @State private var hovering = false

    var body: some View {
        HStack(spacing: 5) {
            icon
            if tab.needsUserAttention {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 9))
                    .foregroundStyle(.orange)
            }
            Text(tab.title)
                .font(.system(size: 12, weight: active ? .semibold : .regular))
                .foregroundStyle(active ? Color.primary : Color.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: 150, alignment: .leading)
                .fixedSize(horizontal: true, vertical: false)
            if hovering && canClose {
                Button(action: onClose) {
                    Image(systemName: "xmark")
                        .font(.system(size: 8, weight: .bold))
                        .foregroundStyle(.secondary)
                        .frame(width: 14, height: 14)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help("关闭")
            }
        }
        .padding(.leading, 9)
        .padding(.trailing, hovering && canClose ? 5 : 9)
        .frame(height: 26)
        .background(
            RoundedRectangle(cornerRadius: 13, style: .continuous)
                .fill(active ? Color.primary.opacity(0.14) : Color.primary.opacity(0.05))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 13, style: .continuous)
                .stroke(Color.white.opacity(active ? 0.28 : 0.16), lineWidth: 0.5)
        )
        .contentShape(Capsule())
        .onHover { hovering = $0 }
        .onTapGesture { onSelect() }
        .help(tab.kind == .home ? "首页" : tab.title)
    }

    @ViewBuilder
    private var icon: some View {
        #if os(macOS)
        if let favicon {
            Image(nsImage: favicon)
                .resizable()
                .interpolation(.medium)
                .frame(width: 12, height: 12)
        } else {
            symbolIcon
        }
        #else
        symbolIcon
        #endif
    }

    private var symbolIcon: some View {
        Image(systemName: tab.icon)
            .font(.system(size: 10.5, weight: .medium))
            .foregroundStyle(active ? Color("appAccent") : Color.secondary)
    }
}
