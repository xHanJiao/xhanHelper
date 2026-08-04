import Foundation
import WebKit
import Combine

/// 页内查找协调器（Cmd+F 唤出 / Cmd+G 下一个 / Cmd+Shift+G 上一个 / Esc 关闭）。
/// 单例共享：非激活 tab 已摘除视图层级，任一时刻仅激活 tab 的 BrowserTabView 在监听。
/// WKFindResult 仅暴露 matchFound（无计数），因此查找栏只显示命中与否。
@MainActor
final class BrowserFindCoordinator: ObservableObject {
    static let shared = BrowserFindCoordinator()

    @Published var isPresented = false
    @Published var query = ""
    /// 最近一次查找是否命中（true=有匹配，false=无匹配）
    @Published var matchFound = false

    private weak var webView: WKWebView?

    private init() {}

    /// 激活 tab 挂载时绑定其 webView（不弹出栏）
    func attach(_ wv: WKWebView) {
        webView = wv
    }

    /// Cmd+F：绑定并唤出查找栏
    func requestShow(_ wv: WKWebView) {
        webView = wv
        isPresented = true
    }

    /// Esc：关闭并清空
    func close() {
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
        guard let webView, !query.isEmpty else {
            matchFound = false
            return
        }
        let config = WKFindConfiguration()
        config.backwards = !forward
        config.wraps = true
        webView.find(query, configuration: config) { [weak self] result in
            self?.matchFound = result.matchFound
        }
    }
}
