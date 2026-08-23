import SwiftUI

/// 浏览器收藏夹栏：地址栏下方一行收藏项。左键打开，右键删除/隐藏。
/// 文件夹：点击浮层展开子项（可拖出）；拖书签到文件夹上归类；空白处右键新建文件夹。
struct BookmarksBarView: View {
    @ObservedObject private var store = BookmarkStore.shared
    @ObservedObject private var barState = BookmarksBarState.shared
    @ObservedObject private var faviconStore = FaviconStore.shared
    @State private var renaming: BookmarkStore.Bookmark?
    @State private var renameText = ""
    @State private var deletingFolder: BookmarkStore.Bookmark?

    var body: some View {
        HStack(spacing: 6) {
            if store.bookmarks.isEmpty {
                emptyHint
            } else {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 2) {
                        ForEach(store.bookmarks) { bookmark in
                            if bookmark.isFolder {
                                FolderBarItem(
                                    folder: bookmark,
                                    children: store.children(of: bookmark.id),
                                    onRename: {
                                        renaming = bookmark
                                        renameText = bookmark.title
                                    },
                                    onRequestDelete: { deletingFolder = bookmark }
                                )
                            } else if bookmark.folderID == nil {
                                bookmarkItem(bookmark)
                                    .bookmarkReorderable(bookmark, axis: .horizontal)
                            }
                        }
                    }
                }
            }
            Spacer(minLength: 4)
            manageButton
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        // 显式直角 Rectangle：隔离外层卡片 clipShape 圆角对材质的渲染影响（收藏夹栏保持直角）
        .background(Rectangle().fill(.ultraThinMaterial))
        // 空白处落点：拖子项到栏上空白区域 → 移出文件夹归回顶层（拖到条目上由条目处理）
        .dropDestination(for: String.self) { ids, _ in
            guard let s = ids.first, let dragged = UUID(uuidString: s) else { return false }
            Task { @MainActor in
                BookmarkStore.shared.moveToFolder(id: dragged, folderID: nil)
            }
            return true
        }
        .contextMenu {
            Button("新建文件夹") {
                let id = store.createFolder()
                if let f = store.bookmarks.first(where: { $0.id == id }) {
                    renaming = f
                    renameText = f.title
                }
            }
            Button("隐藏收藏夹栏") { barState.isVisible = false }
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

    private var emptyHint: some View {
        Text("收藏夹为空 · 点击地址栏星标收藏网页")
            .font(.system(size: 11))
            .foregroundColor(.secondary)
    }

    private var manageButton: some View {
        Button {
            BrowserToolProvider.shared.rightSidebar?.openBookmarkTab()
        } label: {
            Image(systemName: "bookmark")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
                .frame(width: 22, height: 18)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("打开收藏夹")
    }

    private func bookmarkItem(_ b: BookmarkStore.Bookmark) -> some View {
        Button {
            guard let url = URL(string: b.url) else { return }
            BrowserToolProvider.shared.openURL(url)
        } label: {
            HStack(spacing: 4) {
                favicon(for: b)
                if !b.title.isEmpty {
                    Text(b.title)
                        .font(.system(size: 11))
                        .lineLimit(1)
                }
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(b.url)
        .contextMenu {
            Button("打开") {
                guard let url = URL(string: b.url) else { return }
                BrowserToolProvider.shared.openURL(url)
            }
            Button("重命名") {
                renaming = b
                renameText = b.title
            }
            Button("删除收藏", role: .destructive) { store.remove(id: b.id) }
        }
    }

    @ViewBuilder
    private func favicon(for b: BookmarkStore.Bookmark) -> some View {
        if let img = faviconStore.image(forHost: b.host) {
            #if os(macOS)
            Image(nsImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 13, height: 13)
            #else
            Image(uiImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 13, height: 13)
            #endif
        } else {
            Image(systemName: "bookmark")
                .font(.system(size: 9))
                .foregroundColor(.secondary)
        }
    }
}

/// 收藏夹栏上的文件夹项：点击浮层展示子项（子项可拖出归位顶层），拖入书签归类，自身可拖拽重排。
private struct FolderBarItem: View {
    let folder: BookmarkStore.Bookmark
    let children: [BookmarkStore.Bookmark]
    let onRename: () -> Void
    let onRequestDelete: () -> Void

    @State private var isMenuOpen = false
    @State private var isDropTargeted = false

    var body: some View {
        Button {
            isMenuOpen.toggle()
        } label: {
            HStack(spacing: 4) {
                Image(systemName: "folder.fill")
                    .font(.system(size: 10))
                    .foregroundColor(isDropTargeted ? .accentColor : .secondary)
                if !folder.title.isEmpty {
                    Text(folder.title)
                        .font(.system(size: 11))
                        .lineLimit(1)
                }
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("文件夹 · 点击展开")
        .popover(isPresented: $isMenuOpen) {
            folderPopover
        }
        // 拖拽重排（复用相邻项的 dropDestination）；拖到别的文件夹上会被 moveToFolder 拒绝
        .draggable(folder.id.uuidString)
        // 接受书签拖入 → 归类进文件夹
        .dropDestination(for: String.self) { ids, _ in
            guard let s = ids.first, let dragged = UUID(uuidString: s), dragged != folder.id else { return false }
            let target = folder.id
            Task { @MainActor in
                BookmarkStore.shared.moveToFolder(id: dragged, folderID: target)
            }
            return true
        } isTargeted: { isDropTargeted = $0 }
        .contextMenu {
            Button("重命名") { onRename() }
            Button("删除文件夹", role: .destructive) { onRequestDelete() }
        }
    }

    /// 浮层：子项列表，每项可点击打开、可拖出文件夹。
    private var folderPopover: some View {
        VStack(alignment: .leading, spacing: 2) {
            if children.isEmpty {
                Text("空文件夹")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .padding(8)
            }
            ForEach(children) { kid in
                HStack(spacing: 6) {
                    folderFavicon(for: kid)
                    Text(kid.title.isEmpty ? kid.host : kid.title)
                        .font(.system(size: 12))
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer(minLength: 8)
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 5)
                .contentShape(Rectangle())
                .draggable(kid.id.uuidString)
                .onTapGesture {
                    if let url = URL(string: kid.url) {
                        BrowserToolProvider.shared.openURL(url)
                    }
                    isMenuOpen = false
                }
            }
        }
        .padding(4)
        .frame(minWidth: 160, maxWidth: 220)
    }

    @ViewBuilder
    private func folderFavicon(for b: BookmarkStore.Bookmark) -> some View {
        if let img = FaviconStore.shared.image(forHost: b.host) {
            #if os(macOS)
            Image(nsImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 13, height: 13)
            #else
            Image(uiImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 13, height: 13)
            #endif
        } else {
            Image(systemName: "globe")
                .font(.system(size: 10))
                .foregroundColor(.secondary)
        }
    }
}
