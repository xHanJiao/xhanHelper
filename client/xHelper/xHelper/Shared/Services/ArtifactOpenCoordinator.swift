import Combine
import SwiftUI

/// 打开一条 HTML 产物：拉正文 → 在右栏开 tab（macOS）/ sheet（iOS）。
///
/// 每次点都重新拉，不做缓存：artifact 不可变，缓存只会多一套失效逻辑
/// （docs/design/html-artifact §十一）。
///
/// **macOS 先开骨架 tab 再换内容**：大页面（内联多图的将近 8MB）拉取 + 解析要几秒，
/// 「拉完才开 tab」在慢链路上就是纯静默，看起来像点了没反应（§9.8）。
@MainActor
final class ArtifactOpenCoordinator: ObservableObject {
    static let shared = ArtifactOpenCoordinator()

    /// macOS 右栏（ContentView 注入）
    weak var rightSidebar: RightSidebarManager?
    /// iOS 用 sheet 展示当前这篇
    @Published var presented: ArtifactPayload?

    private var baseURL = ""
    private var token = ""

    func configure(baseURL: String, token: String) {
        self.baseURL = baseURL
        self.token = token
    }

    private var api: APIClient { APIClient(baseURL: baseURL, token: token) }

    private func tabID(_ id: String) -> String {
        "\(RightSidebarManager.artifactTabPrefix)\(id)"
    }

    func open(id: String, title: String) {
        let tabID = tabID(id)
        #if os(macOS)
        guard let rs = rightSidebar else {
            ToastCenter.shared.show("右侧栏还没准备好，稍后再试")
            return
        }
        // 同一个 id 已开着：只切过去，不重建（重建会把已渲染的页面扔掉再加载一遍）
        let alreadyOpen = rs.tabs.contains { $0.id == tabID }
        rs.openTab(
            id: tabID,
            title: title,
            icon: "doc.richtext",
            content: AnyView(ArtifactLoadingView(title: title))
        )
        guard !alreadyOpen else { return }
        #endif
        Task { await load(id: id, title: title, tabID: tabID) }
    }

    private func load(id: String, title: String, tabID: String) async {
        do {
            let payload = try await api.getArtifact(id: id)
            #if os(macOS)
            let shown = payload.title.isEmpty ? title : payload.title
            rightSidebar?.updateTabTitle(id: tabID, title: shown)
            rightSidebar?.updateTabContent(
                id: tabID,
                content: AnyView(HTMLArtifactView(id: payload.id, title: shown, html: payload.html))
            )
            #else
            presented = payload
            #endif
        } catch {
            LogBuffer.shared.error("ArtifactOpenCoordinator.open id=\(id): \(error)")
            let deleted = (error as NSError).code == 404
            let message = deleted ? "这个页面已被删除。" : error.localizedDescription
            #if os(macOS)
            // 骨架是临时占位：拉失败就关掉，别留一个永远白屏的 tab（原因由 toast 说清）
            rightSidebar?.closeTab(tabID)
            #endif
            ToastCenter.shared.show(deleted ? "这个页面已被删除" : "打不开页面：\(message)")
        }
    }

    /// 改版之后：页面若正开在右栏（macOS tab / iOS sheet），重新拉一遍正文。
    ///
    /// 原地覆盖不会产生新 id，所以点卡片走的还是 `open` 的「已开着就直接切过去」分支，
    /// 用户会以为没改成（review P1）。这里补一次静默重载（不显示骨架，不闪）。
    func reloadIfOpen(id: String, title: String) {
        #if os(macOS)
        let tab = tabID(id)
        guard let rs = rightSidebar, rs.tabs.contains(where: { $0.id == tab }) else { return }
        Task { await load(id: id, title: title, tabID: tab) }
        #else
        guard presented?.id == id else { return }
        Task { await load(id: id, title: title, tabID: tabID(id)) }
        #endif
    }

    /// 删除产物时关掉它可能开着的 tab（`ArtifactViewModel.delete` 调）。
    func closeTabIfOpen(id: String) {
        #if os(macOS)
        rightSidebar?.closeTab(tabID(id))
        #endif
    }

    // MARK: - 分享（docs/design/html-artifact §9.6）

    /// 生成或复用分享链接，返回可复制的绝对地址。失败时 toast 并返回 nil。
    func share(id: String) async -> String? {
        do {
            let share = try await api.shareArtifact(id: id)
            var host = baseURL
            while host.hasSuffix("/") { host.removeLast() }
            return host + share.url
        } catch {
            LogBuffer.shared.error("ArtifactOpenCoordinator.share id=\(id): \(error)")
            ToastCenter.shared.show("生成分享链接失败：\(error.localizedDescription)")
            return nil
        }
    }

    /// 撤销分享：旧链接立即失效。返回是否真的撤销了（nil＝请求失败，false＝本来没分享）。
    func unshare(id: String) async -> Bool? {
        do {
            let resp = try await api.unshareArtifact(id: id)
            return resp.revoked
        } catch {
            LogBuffer.shared.error("ArtifactOpenCoordinator.unshare id=\(id): \(error)")
            ToastCenter.shared.show("撤销分享失败：\(error.localizedDescription)")
            return nil
        }
    }
}
