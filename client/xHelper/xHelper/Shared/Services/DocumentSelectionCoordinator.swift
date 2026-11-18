import Foundation
import Combine
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

/// 文档选中状态协调器：追踪当前选中的文档（DocumentFullView 正在展示）。
/// 顶栏文档按钮组按 TopBarRegistry 通用契约声明式显隐：
/// 注册一次 + visible 闭包判「右栏正显示文档详情」+ 选择变化调 refresh()。
/// 右栏状态（browsePanelSelection / activeTabId / isOpen）由 TopBarView 观察回自动重算。
@MainActor
final class DocumentSelectionCoordinator: ObservableObject {
    static let shared = DocumentSelectionCoordinator()

    /// 右侧栏管理器（弱引用，由 ContentView 注入），用于判断文档页是否恰好在右栏前台。
    weak var rightSidebar: RightSidebarManager?

    /// 文档视图模型（弱引用，由 ContentView 注入）。按 id 打开文档时写入 openDocument。
    weak var documentVM: DocumentViewModel?

    /// API 访问信息（ContentView 注入，分享文档用）
    private var baseURL = ""
    private var token = ""
    private var isCreatingShare = false

    @Published private(set) var selectedDocument: Document?

    private var groupRegistered = false

    private init() {}

    func select(_ document: Document) {
        selectedDocument = document
        TopBarRegistry.shared.refresh()
    }

    func deselect() {
        selectedDocument = nil
        TopBarRegistry.shared.refresh()
    }

    /// 解析内部文档跳链 `xhanhelper://document/<id>`，非该形态返回 nil。
    /// 聊天 Markdown（SwiftUI `.openURL`）与文档正文（WKWebView 导航）两条路共用，避免解析分叉。
    nonisolated static func documentID(from url: URL) -> String? {
        guard url.scheme == "xhanhelper", url.host == "document",
              let id = url.pathComponents.dropFirst().first, !id.isEmpty else { return nil }
        return id
    }

    /// 按 id 打开文档：拉取 → 开（或激活）文档详情 tab。
    /// 入口有两个——聊天里的文档卡片、文档正文里的内链（`xhanhelper://document/<id>`）。
    /// id 非法或已删除时就地提示，不让链接点下去无声无息。
    func openDocument(id: String) {
        guard let rs = rightSidebar else { return }
        Task { @MainActor in
            do {
                let doc = try await APIClient(baseURL: baseURL, token: token).getDocument(id: id)
                rs.isOpen = true
                rs.openDocumentTab(doc)
            } catch {
                LogBuffer.shared.error("DocumentSelectionCoordinator.openDocument: \(error)")
                ToastCenter.shared.show("打不开文档：\(error.localizedDescription)")
            }
        }
    }

    /// 注入 API 访问信息（ContentView 设置，分享文档用）
    func configure(baseURL: String, token: String) {
        self.baseURL = baseURL
        self.token = token
    }

    /// 注册顶栏文档组（幂等，参照浏览器组「注册一次 + visible 闭包」模式）。
    func ensureRegisteredGroup() {
        guard !groupRegistered else { return }
        groupRegistered = true
        TopBarRegistry.shared.register(TopBarGroup(
            id: "document-actions",
            slot: .center,
            priority: 8,
            buttons: [
                TopBarButton(id: "document-annot", icon: { "list.bullet.rectangle" },
                             enabled: { true },
                             action: {
                                 let key = Self.annotOpenKey
                                 UserDefaults.standard.set(!Self.readBool(key, default: true), forKey: key)
                             },
                             help: { "划线与批注面板" }),
                TopBarButton(id: "document-find", icon: { "magnifyingglass" },
                             enabled: { true },
                             action: {
                                 let key = Self.findOpenKey
                                 UserDefaults.standard.set(!Self.readBool(key, default: false), forKey: key)
                             },
                             help: { "在文档中查找（⌘F）" },
                             keyEquivalent: ("f", .command)),
                TopBarButton(id: "document-download", icon: { "arrow.down.circle" },
                             enabled: { true },
                             action: { [weak self] in
                                 guard let doc = self?.selectedDocument else { return }
                                 self?.download(doc)
                             },
                             help: { "下载文档 (.md)" }),
                TopBarButton(id: "document-share", icon: { "link" },
                             enabled: { true },
                             action: { [weak self] in
                                 guard let doc = self?.selectedDocument else { return }
                                 self?.share(doc)
                             },
                             help: { "分享文档" }),
            ],
            visible: { [weak self] in self?.isDocumentDetailVisible ?? false },
            // 放不下时允许"留一部分 + 末尾 ⋯"（自动省略），见 docs/design/overflow-collapse
            autoOverflow: true
        ))
    }

    /// 当前是否应显示文档按钮组：右栏前台正停在文档详情 tab。
    private var isDocumentDetailVisible: Bool {
        guard let rs = rightSidebar, selectedDocument != nil else { return false }
        return rs.isOpen && rs.activeTab?.kind == .document
    }

    /// 详情 tab 的「划线与批注 / 查找」按钮：与 `DocumentFullView` 读写同两个 key。
    private static let annotOpenKey = "xhan_doc_annot_open"
    private static let findOpenKey = "xhan_doc_find_open"

    private static func readBool(_ key: String, default defaultValue: Bool) -> Bool {
        (UserDefaults.standard.object(forKey: key) as? Bool) ?? defaultValue
    }

    /// 下载文档为 .md 文件（macOS NSSavePanel，与账单 CSV 导出同模式）
    func download(_ doc: Document) {
        #if os(macOS)
        let fileName = (doc.title.isEmpty ? doc.id : doc.title) + ".md"
        let panel = NSSavePanel()
        panel.title = "下载文档"
        panel.nameFieldStringValue = fileName
        panel.allowedContentTypes = [UTType(importedAs: "public.markdown")]
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            do {
                try doc.content.write(to: url, atomically: true, encoding: .utf8)
                ToastCenter.shared.show("已下载 \(url.lastPathComponent)")
            } catch {
                LogBuffer.shared.error("document download failed: \(error.localizedDescription)")
            }
        }
        #endif
    }

    /// 导出文档为排版 PDF（macOS NSSavePanel + WebKit 分页导出）。
    /// 顶栏按钮传 `selectedDocument`，卡片「⋯」传自己那张 —— 两处共用。
    func exportPDF(_ doc: Document) {
        #if os(macOS)
        let fileName = (doc.title.isEmpty ? doc.id : doc.title) + ".pdf"
        let panel = NSSavePanel()
        panel.title = "导出 PDF"
        panel.nameFieldStringValue = fileName
        panel.allowedContentTypes = [.pdf]
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task { @MainActor in
                do {
                    try await DocumentPDFExporter.export(doc, to: url, baseURL: URL(string: self.baseURL))
                    ToastCenter.shared.show("已导出 \(url.lastPathComponent)")
                } catch {
                    print("[PDFExport] coordinator catch: \(error)")
                    LogBuffer.shared.error("document pdf export failed: \(error.localizedDescription)")
                    ToastCenter.shared.show("导出 PDF 失败")
                }
            }
        }
        #endif
    }

    /// 分享文档：先查已有分享链接（重复点击不重置），没有则创建；
    /// 把完整链接复制到剪贴板并 Toast 提示（对齐 Web 端行为，跳过弹窗）。
    func share(_ doc: Document) {
        guard !isCreatingShare else { return }
        #if os(macOS)
        isCreatingShare = true
        let api = APIClient(baseURL: baseURL, token: token)
        Task {
            do {
                let share: DocumentShareResult
                do {
                    share = try await api.getDocumentShare(docID: doc.id)
                } catch {
                    share = try await api.createDocumentShare(docID: doc.id)
                }
                // 分享链接指向前端源（本地开发 5173、线上同源），与后端 API 地址分离
                let fullURL = "\(DeviceManager.webBaseURL)\(share.url)"
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(fullURL, forType: .string)
                isCreatingShare = false
                ToastCenter.shared.show("分享链接已复制")
            } catch {
                isCreatingShare = false
                LogBuffer.shared.error("document share failed: \(error.localizedDescription)")
                ToastCenter.shared.show("分享失败：\(error.localizedDescription)")
            }
        }
        #endif
    }
}