import Combine
import SwiftUI
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

/// iOS sheet 的展示数据。`html` 已注入版式基线（`HTMLArtifactView` 不再自己注入）。
struct OpenedArtifactPage: Identifiable {
    let id: String
    let title: String
    let html: String
}

/// 打开一条 HTML 产物：拉正文 → 在右栏开 tab（macOS）/ sheet（iOS）。
///
/// 正文按 ETag 落本地缓存（`ArtifactHTMLCache`）：重开时先秒出缓存、再发一个
/// 条件请求确认（304 不动，200 才换），不用每次重下几 MB（docs/design/html-artifact §9.8）。
///
/// **macOS 先开 tab 再换内容**：没缓存时大页面（内联多图的将近 8MB）拉取要几秒，
/// 「拉完才开 tab」在慢链路上就是纯静默，看起来像点了没反应（§9.8）。
@MainActor
final class ArtifactOpenCoordinator: ObservableObject {
    static let shared = ArtifactOpenCoordinator()

    /// macOS 右栏（ContentView 注入）
    weak var rightSidebar: RightSidebarManager?
    /// 灵阅列表（ContentView 注入）：详情页左侧换篇栏共用浏览面板那份数据。
    /// 必需依赖，与 `rightSidebar` 同处注入；为 nil 时 `tabContent` 直接炸（接线 bug，不静默降级）。
    weak var artifactVM: ArtifactViewModel?
    /// iOS 用 sheet 展示当前这篇
    @Published var presented: OpenedArtifactPage?

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
            ToastCenter.shared.show("右侧栏尚未就绪，请稍后重试")
            return
        }
        // 同一个 id 已开着：切过去（右栏可能收着或在分屏），不重建 content
        if rs.tabs.contains(where: { $0.id == tabID }) {
            rs.isOpen = true
            rs.activateTab(id: tabID, inRightPane: rs.splitTabIds.contains(tabID))
            return
        }
        // 有缓存就先秒开（随后条件请求确认/更新）；没缓存才挂骨架
        let cached = ArtifactHTMLCache.shared.load(id: id)
        rs.openTab(
            id: tabID,
            title: title,
            icon: "doc.richtext",
            content: tabContent(id: id, title: title, html: cached.map { injectingBaseline($0.html) })
        )
        #else
        // iOS：有缓存就先弹 sheet（无骨架），条件请求随后确认/更新
        let cached = ArtifactHTMLCache.shared.load(id: id)
        if let cached {
            presented = OpenedArtifactPage(id: id, title: title, html: injectingBaseline(cached.html))
        }
        #endif
        Task { await load(id: id, title: title, tabID: tabID, cached: cached) }
    }

    private func load(id: String, title: String, tabID: String, cached: ArtifactHTMLCache.Entry?) async {
        do {
            let resp = try await api.fetchArtifactHTML(id: id, etag: cached?.etag)
            if resp.notModified, cached != nil {
                // 打开时已经把缓存画上去了；304 说明内容没变，不重建 WebView（免得白闪一下）
                ArtifactHTMLCache.shared.touch(id: id)
                LogBuffer.shared.diag("[artifact] id=\(id) cache=hit etag=304")
                return
            }
            var fresh: String
            var etag = resp.etag
            if let html = resp.html {
                fresh = html
                let state = cached == nil ? "miss" : "stale"
                LogBuffer.shared.diag("[artifact] id=\(id) cache=\(state) etag=200 bytes=\(html.utf8.count)")
            } else {
                // 304 但缓存刚好被系统清掉：退回一次完整拉取
                let full = try await api.fetchArtifactHTML(id: id, etag: nil)
                guard let html = full.html else {
                    throw NSError(domain: "ArtifactOpenCoordinator", code: 500,
                        userInfo: [NSLocalizedDescriptionKey: "正文为空"])
                }
                fresh = html
                etag = full.etag
                LogBuffer.shared.diag("[artifact] id=\(id) cache=refetch bytes=\(html.utf8.count)")
            }
            do {
                try ArtifactHTMLCache.shared.store(id: id, html: fresh, etag: etag)
            } catch {
                // 缓存失败不影响本次展示，下次退回完整下载
                LogBuffer.shared.error("ArtifactOpenCoordinator.cache id=\(id): \(error)")
            }
            show(id: id, title: title, tabID: tabID, html: fresh)
        } catch {
            let ns = error as NSError
            if ns.code == 404 {
                // 产物已删：缓存也要清，别让聊天里的旧卡片还能翻出来
                ArtifactHTMLCache.shared.remove(ids: [id])
                LogBuffer.shared.error("ArtifactOpenCoordinator.open id=\(id): \(error)")
                #if os(macOS)
                rightSidebar?.closeTab(tabID)
                #else
                if presented?.id == id { presented = nil }
                #endif
                ToastCenter.shared.show("该页面已被删除")
                return
            }
            // 网络失败：有缓存就继续看缓存（打开时已经画上去了），记日志、不打扰
            if cached != nil {
                LogBuffer.shared.error("ArtifactOpenCoordinator.open id=\(id) 网络失败，显示缓存: \(error)")
                return
            }
            LogBuffer.shared.error("ArtifactOpenCoordinator.open id=\(id): \(error)")
            #if os(macOS)
            // 骨架是临时占位：拉失败就关掉，别留一个永远白屏的 tab（原因由 toast 说清）
            rightSidebar?.closeTab(tabID)
            #endif
            ToastCenter.shared.show("无法打开页面：\(error.localizedDescription)")
        }
    }

    /// tab 内容（macOS）：必带左栏换篇（骨架态也一样，见 `ArtifactDetailView(html: nil)`）。
    #if os(macOS)
    private func tabContent(id: String, title: String, html: String?) -> AnyView {
        AnyView(ArtifactDetailView(id: id, title: title, html: html, viewModel: requiredArtifactVM))
    }

    /// 必需依赖：`artifactVM` 与 `rightSidebar` 在同一处（`ContentView.onAppear`）注入，
    /// 走到这里还是 nil 就是接线 bug —— 让它炸在明处，别降级成一个「看起来正常、功能缺一半」的页面。
    private var requiredArtifactVM: ArtifactViewModel {
        guard let artifactVM else {
            fatalError("ArtifactOpenCoordinator: artifactVM 未注入（应在 ContentView.onAppear 接线）")
        }
        return artifactVM
    }
    #endif

    /// 版式基线只在这里注入一次，视图拿到的就是最终要渲染的字符串。
    private func show(id: String, title: String, tabID: String, html: String) {
        let page = injectingBaseline(html)
        #if os(macOS)
        rightSidebar?.updateTabTitle(id: tabID, title: title)
        rightSidebar?.updateTabContent(
            id: tabID,
            content: tabContent(id: id, title: title, html: page)
        )
        #else
        presented = OpenedArtifactPage(id: id, title: title, html: page)
        #endif
    }

    /// 改版之后：页面若正开在右栏（macOS tab / iOS sheet），重新拉一遍正文。
    ///
    /// 原地覆盖不会产生新 id，所以点卡片走的还是 `open` 的「已开着就直接切过去」分支，
    /// 用户会以为没改成（review P1）。这里补一次静默重载（不显示骨架，不闪）。
    func reloadIfOpen(id: String, title: String) {
        #if os(macOS)
        let tab = tabID(id)
        guard let rs = rightSidebar, rs.tabs.contains(where: { $0.id == tab }) else { return }
        let cached = ArtifactHTMLCache.shared.load(id: id)
        Task { await load(id: id, title: title, tabID: tab, cached: cached) }
        #else
        guard presented?.id == id else { return }
        let cached = ArtifactHTMLCache.shared.load(id: id)
        Task { await load(id: id, title: title, tabID: tabID(id), cached: cached) }
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

    // MARK: - 卡片「⋯」菜单（docs/design/card-more-menu）

    /// 生成或复用分享链接 → 复制到剪贴板 → toast。失败由 `share(id:)` 自己 toast。
    func copyShareLink(id: String) async {
        guard let url = await share(id: id) else { return }
        copyToPasteboard(url)
        ToastCenter.shared.show("分享链接已复制，获得链接者无需登录即可查看")
    }

    #if os(macOS)
    /// 下载正文为 `.html` 单文件：列表接口不含正文，先按 id 拉。
    /// 顺序是**先弹保存面板再拉正文**（对齐 `DocumentSelectionCoordinator.download`）：
    /// 大页面（内联多图能到 8MB）先静默拉几秒再弹面板，看起来像点了没反应。
    /// 落盘的内容走 `injectingBaseline`——与右栏同一份版式基线，否则 Safari 打开就是 Times + 零边距。
    func downloadHTML(id: String, title: String) async {
        guard let url = saveURL(title: title, ext: "html", contentType: .html) else { return }
        do {
            let html = try await api.getArtifact(id: id).html
            try injectingBaseline(html).write(to: url, atomically: true, encoding: .utf8)
            ToastCenter.shared.show("已下载 \(url.lastPathComponent)")
        } catch {
            LogBuffer.shared.error("ArtifactOpenCoordinator.downloadHTML id=\(id): \(error)")
            ToastCenter.shared.show("下载失败：\(error.localizedDescription)")
        }
    }

    /// 导出正文为排版 PDF：复用 `DocumentPDFExporter` 的打印分页（与文档导出同一条管线）。
    /// 同样先面板后拉正文；`waitForImages` 让打印前等内联 base64 大图解码完（见 `exportHTML`）。
    func exportPDF(id: String, title: String) async {
        guard let url = saveURL(title: title, ext: "pdf", contentType: .pdf) else { return }
        do {
            let html = try await api.getArtifact(id: id).html
            // 与屏幕同一份版式基线，否则 PDF 和右栏观感不一致
            try await DocumentPDFExporter.exportHTML(injectingBaseline(html), to: url)
            ToastCenter.shared.show("已导出 \(url.lastPathComponent)")
        } catch {
            LogBuffer.shared.error("ArtifactOpenCoordinator.exportPDF id=\(id): \(error)")
            ToastCenter.shared.show("导出 PDF 失败：\(error.localizedDescription)")
        }
    }

    /// 统一的保存面板；用户取消返回 nil。
    private func saveURL(title: String, ext: String, contentType: UTType) -> URL? {
        let panel = NSSavePanel()
        panel.title = ext == "pdf" ? "导出 PDF" : "下载页面"
        panel.nameFieldStringValue = (title.isEmpty ? "页面" : title) + ".\(ext)"
        panel.allowedContentTypes = [contentType]
        return panel.runModal() == .OK ? panel.url : nil
    }
    #endif
}
