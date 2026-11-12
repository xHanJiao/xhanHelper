import SwiftUI
#if os(macOS)
import AppKit
import UniformTypeIdentifiers

/// 收藏夹面板（浏览器内特殊 tab）：列表 + 搜索 + 导入/导出。
struct BookmarkPanelView: View {
    @ObservedObject private var store = BookmarkStore.shared
    @ObservedObject private var faviconStore = FaviconStore.shared
    @State private var query = ""
    @State private var renaming: BookmarkStore.Bookmark?
    @State private var renameText = ""
    @State private var deletingFolder: BookmarkStore.Bookmark?
    @State private var expandedFolders: Set<UUID> = []
    @State private var dropTargetFolderID: UUID?

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if displayItems.isEmpty {
                emptyState
            } else {
                ScrollView {
                    LazyVStack(spacing: 8) {
                        ForEach(displayItems) { item in
                            if item.isFolder {
                                folderSection(item)
                            } else {
                                panelRow(item)
                            }
                        }
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                }
                // 空白处落点：拖子项到空白区域 → 移回顶层（拖到条目上由条目处理）
                .dropDestination(for: String.self) { ids, _ in
                    guard let s = ids.first, let dragged = UUID(uuidString: s) else { return false }
                    Task { @MainActor in
                        BookmarkStore.shared.moveToFolder(id: dragged, folderID: nil)
                    }
                    return true
                }
            }
        }
        .alert(renaming?.isFolder == true ? "重命名文件夹" : "重命名收藏", isPresented: Binding(
            get: { renaming != nil },
            set: { if !$0 { renaming = nil } }
        )) {
            TextField("名称（留空则只显示图标）", text: $renameText)
            Button("取消", role: .cancel) { }
            Button("保存") {
                if let b = renaming { store.rename(id: b.id, title: renameText) }
            }
        }
        .confirmationDialog(
            deletingFolder.map {
                "删除文件夹「\($0.title)」将同时删除其中 \(store.children(of: $0.id).count) 个收藏。"
            } ?? "",
            isPresented: Binding(
                get: { deletingFolder != nil },
                set: { if !$0 { deletingFolder = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("删除文件夹", role: .destructive) {
                if let f = deletingFolder { store.deleteFolder(id: f.id) }
            }
            Button("取消", role: .cancel) { }
        }
    }

    /// 空搜索：顶层条目（含文件夹，子项随展开内联显示）；搜索：全部匹配的非文件夹条目。
    private var displayItems: [BookmarkStore.Bookmark] {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if q.isEmpty {
            return store.bookmarks.filter { $0.isFolder || $0.folderID == nil }
        }
        return store.bookmarks.filter {
            !$0.isFolder && ($0.title.lowercased().contains(q) || $0.url.lowercased().contains(q))
        }
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
            Button {
                let id = store.createFolder()
                if let f = store.bookmarks.first(where: { $0.id == id }) {
                    renaming = f
                    renameText = f.title
                }
            } label: {
                Image(systemName: "folder.badge.plus")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("新建文件夹")
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

    /// 面板行：搜索状态下顺序由搜索确定，禁止拖拽重排；空搜索才可拖。
    @ViewBuilder
    private func panelRow(_ b: BookmarkStore.Bookmark) -> some View {
        let row = HoverCardRow { rowContent(b) }
        if query.isEmpty {
            row.bookmarkReorderable(b, axis: .vertical)
        } else {
            row
        }
    }

    /// 文件夹区块：文件夹行（点击展开/收起）+ 展开后的子项（缩进）。
    private func folderSection(_ f: BookmarkStore.Bookmark) -> some View {
        let kids = store.children(of: f.id)
        let expanded = expandedFolders.contains(f.id)
        return VStack(spacing: 8) {
            HoverCardRow {
                HStack(spacing: 8) {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundColor(.secondary)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                    Image(systemName: expanded ? "folder.fill" : "folder")
                        .font(.system(size: 14))
                        .foregroundColor(.accentColor)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(f.title.isEmpty ? "未命名文件夹" : f.title)
                            .font(.system(size: 12, weight: .medium))
                            .lineLimit(1)
                        Text("\(kids.count) 个收藏")
                            .font(.system(size: 10))
                            .foregroundColor(.secondary)
                    }
                    Spacer(minLength: 8)
                    Button { deletingFolder = f } label: {
                        Image(systemName: "trash")
                            .font(.system(size: 12))
                            .frame(width: 24, height: 24)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .foregroundColor(.secondary)
                    .help("删除文件夹（连同内容）")
                }
                .padding(.vertical, 2)
                .contentShape(Rectangle())
                .background(Rectangle().fill(Color.accentColor.opacity(dropTargetFolderID == f.id ? 0.15 : 0)))
            }
            .draggable(f.id.uuidString)
            // 拖书签（或子项）进文件夹；文件夹不可入文件夹（moveToFolder 内部拒绝）
            .dropDestination(for: String.self) { ids, _ in
                guard let s = ids.first, let dragged = UUID(uuidString: s), dragged != f.id else { return false }
                let target = f.id
                Task { @MainActor in
                    BookmarkStore.shared.moveToFolder(id: dragged, folderID: target)
                }
                return true
            } isTargeted: { dropTargetFolderID = $0 ? f.id : nil }
            .contextMenu {
                Button("重命名") {
                    renaming = f
                    renameText = f.title
                }
                Button("删除文件夹", role: .destructive) { deletingFolder = f }
            }
            .help(expanded ? "点击收起" : "点击展开")
            .onTapGesture {
                if expanded {
                    expandedFolders.remove(f.id)
                } else {
                    expandedFolders.insert(f.id)
                }
            }
            if expanded {
                ForEach(kids) { kid in
                    HoverCardRow { rowContent(kid) }
                        .padding(.leading, 22)
                        .draggable(kid.id.uuidString)
                }
            }
        }
    }

    private func rowContent(_ b: BookmarkStore.Bookmark) -> some View {
        HStack(spacing: 8) {
            favicon(for: b)
            VStack(alignment: .leading, spacing: 2) {
                Text(b.title)
                    .font(.system(size: 12, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text("\(b.host) · \(SidebarHomeFormat.relativeTime(b.createdAt))")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            Button { store.remove(id: b.id) } label: {
                Image(systemName: "trash")
                    .font(.system(size: 12))
                    .frame(width: 24, height: 24)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("删除收藏")
        }
        .onTapGesture {
            if let url = URL(string: b.url) {
                BrowserToolProvider.shared.openURL(url)
            }
        }
        .contextMenu {
            Button("重命名") {
                renaming = b
                renameText = b.title
            }
            if b.folderID != nil {
                Button("移出文件夹") { store.moveToFolder(id: b.id, folderID: nil) }
            }
            Button("删除收藏", role: .destructive) { store.remove(id: b.id) }
        }
    }

    @ViewBuilder
    private func favicon(for b: BookmarkStore.Bookmark) -> some View {
        if let img = faviconStore.image(forHost: b.host) {
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
}
#endif
