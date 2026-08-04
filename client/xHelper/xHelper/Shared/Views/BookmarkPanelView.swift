import SwiftUI
#if os(macOS)
import AppKit
import UniformTypeIdentifiers

/// 收藏夹面板（浏览器内特殊 tab）：列表 + 搜索 + 导入/导出。
struct BookmarkPanelView: View {
    @ObservedObject private var store = BookmarkStore.shared
    @State private var query = ""

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if filtered.isEmpty {
                emptyState
            } else {
                List {
                    ForEach(filtered) { bookmark in
                        row(bookmark)
                    }
                }
                .listStyle(.plain)
            }
        }
    }

    private var filtered: [BookmarkStore.Bookmark] {
        store.search(query)
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: 8) {
            Text("收藏夹")
                .font(.system(size: 13, weight: .semibold))
            TextField("搜索收藏", text: $query)
                .textFieldStyle(.roundedBorder)
                .font(.system(size: 11))
                .frame(width: 120)
            Spacer()
            Button { importBookmarks() } label: {
                Image(systemName: "square.and.arrow.down")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("导入书签（Chrome/Safari 导出的 HTML）")
            Button { exportBookmarks() } label: {
                Image(systemName: "square.and.arrow.up")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("导出书签（HTML）")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "star")
                .font(.system(size: 30))
                .foregroundColor(.secondary.opacity(0.5))
            Text(query.isEmpty ? "暂无收藏" : "无匹配收藏")
                .font(.system(size: 12))
                .foregroundColor(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Row

    private func row(_ b: BookmarkStore.Bookmark) -> some View {
        HStack(spacing: 8) {
            favicon(for: b)
            VStack(alignment: .leading, spacing: 2) {
                Text(b.title)
                    .font(.system(size: 12, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text("\(b.host) · \(Self.relativeTime(b.createdAt))")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            Button { BrowserToolProvider.shared.openURL(URL(string: b.url)!) } label: {
                Text("打开").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .foregroundColor(.accentColor)
            Button { store.remove(id: b.id) } label: {
                Image(systemName: "trash")
                    .font(.system(size: 10))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("删除收藏")
        }
        .padding(.vertical, 4)
    }

    @ViewBuilder
    private func favicon(for b: BookmarkStore.Bookmark) -> some View {
        if let img = FaviconStore.shared.image(forHost: b.host) {
            Image(nsImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 14, height: 14)
        } else {
            Image(systemName: "bookmark")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
        }
    }

    // MARK: - Import / Export

    private func importBookmarks() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.html]
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            guard let data = try? Data(contentsOf: url) else { return }
            let count = store.importFromHTML(data: data)
            ToastCenter.shared.show(count > 0 ? "导入 \(count) 条书签" : "没有可导入的新书签")
        }
    }

    private func exportBookmarks() {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.html]
        panel.nameFieldStringValue = "bookmarks.html"
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            let html = store.exportHTML()
            try? html.write(to: url, atomically: true, encoding: .utf8)
            ToastCenter.shared.show("已导出到 \(url.lastPathComponent)")
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
