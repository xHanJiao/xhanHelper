import SwiftUI
#if os(macOS)
import AppKit

/// 浏览历史面板（浏览器内特殊 tab）：新→旧平铺，打开/删除/清空。
struct HistoryPanelView: View {
    @ObservedObject private var store = HistoryStore.shared

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
            Button { store.clear() } label: {
                Image(systemName: "trash")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("清空全部历史")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
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
                Text("\(e.host) · \(Self.relativeTime(e.visitedAt))")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            Button { BrowserToolProvider.shared.openURL(URL(string: e.url)!) } label: {
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
        if let img = FaviconStore.shared.image(forHost: e.host) {
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

    private static func relativeTime(_ date: Date) -> String {
        let interval = Date().timeIntervalSince(date)
        if interval < 60 { return "刚刚" }
        if interval < 3600 { return "\(Int(interval / 60)) 分钟前" }
        if interval < 86400 { return "\(Int(interval / 3600)) 小时前" }
        if interval < 7 * 86400 { return "\(Int(interval / 86400)) 天前" }
        let f = DateFormatter()
        f.dateFormat = "yyyy/MM/dd"
        return f.string(from: date)
    }
}
#endif
