import SwiftUI

/// 浏览器收藏夹栏：地址栏下方一行收藏项。左键打开，右键删除/隐藏。
struct BookmarksBarView: View {
    @ObservedObject private var store = BookmarkStore.shared
    @ObservedObject private var barState = BookmarksBarState.shared
    @ObservedObject private var faviconStore = FaviconStore.shared
    @State private var renaming: BookmarkStore.Bookmark?
    @State private var renameText = ""

    var body: some View {
        HStack(spacing: 6) {
            if store.bookmarks.isEmpty {
                emptyHint
            } else {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 2) {
                        ForEach(store.bookmarks) { bookmark in
                            bookmarkItem(bookmark)
                                .bookmarkReorderable(bookmark, axis: .horizontal)
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
        .contextMenu {
            Button("隐藏收藏夹栏") { barState.isVisible = false }
        }
        .alert("重命名收藏", isPresented: Binding(
            get: { renaming != nil },
            set: { if !$0 { renaming = nil } }
        )) {
            TextField("名称（留空则只显示图标）", text: $renameText)
            Button("取消", role: .cancel) { }
            Button("保存") {
                if let b = renaming { store.rename(id: b.id, title: renameText) }
            }
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
