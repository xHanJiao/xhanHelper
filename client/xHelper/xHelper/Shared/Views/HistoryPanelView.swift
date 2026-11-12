import SwiftUI
import WebKit
#if os(macOS)
import AppKit

/// 浏览历史面板（浏览器内特殊 tab）：新→旧平铺，打开/删除/清空。
struct HistoryPanelView: View {
    @ObservedObject private var store = HistoryStore.shared
    @ObservedObject private var faviconStore = FaviconStore.shared

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if store.entries.isEmpty {
                emptyState
            } else {
                List {
                    ForEach(store.entries) { entry in
                        row(entry)
                    }
                }
                .listStyle(.plain)
            }
        }
    }

    private var header: some View {
        HStack(spacing: 8) {
            Text("历史")
                .font(.system(size: 13, weight: .semibold))
            Spacer()
            Button { clearCache() } label: {
                Image(systemName: "externaldrive.badge.xmark")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .hoverTooltip("清除网页缓存（重新打开页面生效）", direction: .down, align: .trailing)
            Button { store.clear() } label: {
                Image(systemName: "trash")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .hoverTooltip("清空全部历史", direction: .down, align: .trailing)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .zIndex(1)   // 抬到 List 之上，否则 .down 的 tooltip 被下方列表盖住
    }

    /// 清除 WKWebView HTTP 缓存（磁盘 + 内存 + 离线缓存）。历史≠缓存，清历史不动缓存。
    private func clearCache() {
        let types: Set<String> = [
            WKWebsiteDataTypeDiskCache,
            WKWebsiteDataTypeMemoryCache,
            WKWebsiteDataTypeOfflineWebApplicationCache,
        ]
        WKWebsiteDataStore.default().removeData(ofTypes: types, modifiedSince: .distantPast) {
            // 已加载的 webview 保持内存态，下次打开/刷新即生效
        }
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "clock")
                .font(.system(size: 30))
                .foregroundColor(.secondary.opacity(0.5))
            Text("暂无历史")
                .font(.system(size: 12))
                .foregroundColor(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func row(_ e: HistoryStore.HistoryEntry) -> some View {
        HStack(spacing: 8) {
            favicon(for: e)
            VStack(alignment: .leading, spacing: 2) {
                Text(e.title)
                    .font(.system(size: 12, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text("\(e.host) · \(SidebarHomeFormat.relativeTime(e.visitedAt))")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            Button {
                guard let url = URL(string: e.url) else {
                    LogBuffer.shared.warning("history: 无效 URL：\(e.url.prefix(120))")
                    ToastCenter.shared.show("链接无效，无法打开")
                    return
                }
                BrowserToolProvider.shared.openURL(url)
            } label: {
                Text("打开").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .foregroundColor(.accentColor)
            Button { store.remove(id: e.id) } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 9, weight: .semibold))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("从历史移除")
        }
        .padding(.vertical, 3)
    }

    @ViewBuilder
    private func favicon(for e: HistoryStore.HistoryEntry) -> some View {
        if let img = faviconStore.image(forHost: e.host) {
            Image(nsImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 14, height: 14)
        } else {
            Image(systemName: "clock")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
        }
    }
}
#endif
