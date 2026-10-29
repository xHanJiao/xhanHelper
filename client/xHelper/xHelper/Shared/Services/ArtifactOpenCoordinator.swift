import Combine
import SwiftUI

/// 打开一条 HTML 产物：拉正文 → 在右栏开 tab（macOS）/ sheet（iOS）。
///
/// 每次点都重新拉，不做缓存：artifact 不可变，缓存只会多一套失效逻辑
/// （docs/design/html-artifact §十一）。
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

    func open(id: String, title: String) {
        Task { await load(id: id, title: title) }
    }

    private func load(id: String, title: String) async {
        do {
            let payload = try await APIClient(baseURL: baseURL, token: token).getArtifact(id: id)
            #if os(macOS)
            guard let rs = rightSidebar else {
                ToastCenter.shared.show("右侧栏还没准备好，稍后再试")
                return
            }
            rs.openTab(
                // 以 id 为键：再点同一张是切回去，不会开第二份
                id: "\(RightSidebarManager.artifactTabPrefix)\(id)",
                title: payload.title.isEmpty ? title : payload.title,
                icon: "doc.richtext",
                content: AnyView(HTMLArtifactView(html: payload.html))
            )
            #else
            presented = payload
            #endif
        } catch {
            LogBuffer.shared.error("ArtifactOpenCoordinator.open id=\(id): \(error)")
            ToastCenter.shared.show("打不开页面：\(error.localizedDescription)")
        }
    }
}
