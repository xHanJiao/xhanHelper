import Foundation
import Combine

/// 浏览器收藏夹栏显隐状态（顶栏按钮 / 右键菜单控制，持久化，重启保留）。
@MainActor
final class BookmarksBarState: ObservableObject {
    static let shared = BookmarksBarState()

    @Published var isVisible: Bool {
        didSet { UserDefaults.standard.set(isVisible, forKey: "xhan_bookmarks_bar_visible") }
    }

    private init() {
        isVisible = UserDefaults.standard.bool(forKey: "xhan_bookmarks_bar_visible")
    }

    func toggle() { isVisible.toggle() }
}
