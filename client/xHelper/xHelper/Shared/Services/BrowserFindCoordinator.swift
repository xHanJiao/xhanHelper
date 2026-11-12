import Foundation
import WebKit
import Combine

/// 页内查找协调器（Cmd+F 唤出 / Cmd+G 下一个 / Cmd+Shift+G 上一个 / Esc 关闭）。
/// 单例共享：非激活 tab 已摘除视图层级，任一时刻仅激活 tab 的 BrowserTabView 在监听。
/// 走内核协议而不是 WKWebView：WK 只能报"命中与否"，CEF 报命中数（都归一成 count）。
@MainActor
final class BrowserFindCoordinator: ObservableObject {
    static let shared = BrowserFindCoordinator()

    @Published var isPresented = false
    @Published var query = ""
    /// 最近一次查找是否命中（true=有匹配，false=无匹配）
    @Published var matchFound = false

    private weak var page: BrowserPage?

    private init() {}

    /// 激活 tab 挂载时绑定其页面（不弹出栏）
    func attach(_ page: BrowserPage) {
        self.page = page
        page.onFindResult = { [weak self] count in
            guard let self else { return }
            self.matchFound = count != 0
        }
    }

    /// Cmd+F：绑定并唤出查找栏
    func requestShow(_ page: BrowserPage) {
        attach(page)
        isPresented = true
    }

    /// Esc：关闭并清空
    func close() {
        page?.stopFinding(clearSelection: true)
        isPresented = false
        query = ""
        matchFound = false
    }

    /// Cmd+G
    func next() {
        find(forward: true)
    }

    /// Cmd+Shift+G
    func previous() {
        find(forward: false)
    }

    /// 输入变化即时搜索
    func queryChanged() {
        find(forward: true)
    }

    private func find(forward: Bool) {
        guard let page, !query.isEmpty else {
            matchFound = false
            return
        }
        page.find(query, forward: forward, matchCase: false)
    }
}
