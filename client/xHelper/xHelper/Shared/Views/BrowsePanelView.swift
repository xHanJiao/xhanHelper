import SwiftUI
import WebKit
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

/// 右侧栏列表/工具面板类型（tab 化后每个 panel 是一个单例 tab）
enum BrowsePanel: String, Identifiable, CaseIterable {
    case bills = "账单"
    #if os(macOS)
    /// 本地 EPUB 书架（解包走系统 `unzip`，仅 macOS）
    case books = "书架"
    #endif
    case reminders = "备忘"
    case memories = "记忆"
    case artifacts = "灵阅"
    case documents = "文档"
    case images = "图片"
    case events = "事件"
    case dashboard = "监控看版"
    case agents = "智能体"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .bills:     return "yensign.circle"
        #if os(macOS)
        case .books:     return "books.vertical"
        #endif
        case .reminders: return "checklist"
        case .memories:  return "brain.head.profile"
        case .artifacts: return "doc.richtext"
        case .documents: return "doc.text"
        case .images:    return "photo.on.rectangle.angled"
        case .events:    return "square.stack.3d.up"
        case .dashboard: return "chart.bar.xaxis"
        case .agents:    return "cpu"
        }
    }

    var help: String {
        switch self {
        case .bills:     return "查看账单记录与统计"
        #if os(macOS)
        case .books:     return "本地 EPUB 书架：导入与阅读"
        #endif
        case .reminders: return "待办提醒（只读，按到期时间排序）"
        case .memories:  return "长期记忆与语义搜索"
        case .artifacts: return "浏览对话中生成的页面、报告与对比图"
        case .documents: return "Markdown 文档与版本"
        case .images:    return "图片库：预览与清理已上传的图"
        case .events:    return "记忆聚合出的事件"
        case .dashboard: return "前端监控看板（成本统计等）"
        case .agents:    return "自定义智能体管理"
        }
    }

    /// 首页展示的入口（浏览器单列在最前，不在此列）。
    /// 记忆 / 图片 / 智能体 / 监控看板已移出首页，改从设置「内容与工具」进入。
    /// 见 docs/design/right-sidebar-home/design.md §7。
    static let homePanels: [BrowsePanel] = {
        #if os(macOS)
        return [.bills, .books, .artifacts, .documents, .events]
        #else
        return [.bills, .reminders, .artifacts, .documents, .events]
        #endif
    }()
}

/// 回收站批量操作（页头按钮 → 二次确认）
private enum TrashBulkAction {
    case restoreAll
    case purgeAll
}

/// 通用多选工具条：取消（Esc）/ 分隔线 / 「已选 N <unit>」/ 右侧自定义动作。
private struct SelectionToolbar<Trailing: View>: View {
    let count: Int
    let unit: String
    let emptyText: String
    let cancelHelp: String
    let onCancel: () -> Void
    @ViewBuilder let trailing: () -> Trailing

    var body: some View {
        HStack(spacing: 8) {
            Button(action: onCancel) {
                HStack(spacing: 3) {
                    Image(systemName: "xmark")
                        .font(.system(size: 11, weight: .semibold))
                    Text("取消")
                        .font(.system(size: 12))
                }
                .foregroundColor(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .keyboardShortcut(.escape, modifiers: [])
            .help(cancelHelp)

            Divider()
                .frame(height: 14)

            Text(count == 0 ? emptyText : "已选 \(count) \(unit)")
                .font(.system(size: 13, weight: .semibold))

            Spacer()

            trailing()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }
}

/// 一个列表/工具 tab 的内容（原 `BrowsePanelView` 的页面部分）。
/// 页头的返回/标题已随 tab 条一起消失；刷新/多选/导入/导出在顶栏 `tab-actions` 组，
/// 这里只留「模式 + 搜索 + 层级」这类视图内状态（见 docs/design/right-sidebar-unified-tabs §3）。
struct PanelTabView: View {
    let panel: BrowsePanel
    @ObservedObject var billVM: BillViewModel
    @ObservedObject var memoryVM: MemoryViewModel
    @ObservedObject var artifactVM: ArtifactViewModel
    @ObservedObject var documentVM: DocumentViewModel
    @ObservedObject var eventVM: EventViewModel
    @ObservedObject var agentVM: AgentViewModel
    @ObservedObject var imageVM: ImageViewModel
    @ObservedObject var homeVM: SidebarHomeViewModel
    let baseURL: String
    let token: String
    /// 卡片点开详情：交给 manager 开独立 tab
    let onOpenDocument: (Document) -> Void
    let onOpenBook: (BookMeta) -> Void

    #if os(macOS)
    @ObservedObject private var bookVM = BookViewModel.shared
    #endif

    @StateObject private var dashboardWebModel = XHWebViewModel()
    @State private var dashboardBust = Int(Date().timeIntervalSince1970)

    /// 文档/灵阅/账单页头搜索图标展开态 + 草稿（同一时刻至多开一个）
    @State private var docSearchOpen = false
    @State private var billSearchOpen = false
    @State private var docSearchDraft = ""
    @State private var billSearchDraft = ""
    @State private var artifactSearchOpen = false
    @State private var artifactSearchDraft = ""

    @State private var isConfirmingBatchDelete = false
    @State private var isConfirmingImageBatchDelete = false
    @State private var isConfirmingArtifactBatchDelete = false
    @State private var trashBulkAction: TrashBulkAction?

    private var dashboardURL: URL? {
        URL(string: DeviceManager.webBaseURL + "/admin?t=\(dashboardBust)")
    }

    private var dashboardConfig: WKWebViewConfiguration {
        let externalLink = XHWebViewBridge.ScriptMessageHandler { body in
            guard let urlString = (body as? [String: Any])?["url"] as? String,
                  urlString.count <= 4096,
                  let url = URL(string: urlString),
                  let scheme = url.scheme?.lowercased(),
                  scheme == "http" || scheme == "https" else { return }
            #if os(macOS)
            NSWorkspace.shared.open(url)
            #endif
        }
        return XHWebViewBridge.makeConfiguration(
            bridgeObjectName: "XH_BRIDGE",
            dictionary: [
                "baseURL": baseURL,
                "platform": "macOS",
                "token": token,
            ],
            messageHandlers: [("externalLink", externalLink)]
        )
    }

    private var dashboardAllowedOrigins: [XHWebViewOrigin] {
        XHWebViewOrigin.frontendOrigins(webBaseURL: DeviceManager.webBaseURL)
    }

    var body: some View {
        VStack(spacing: 0) {
            if isArtifactSelectionActive {
                artifactSelectionHeader
            } else {
                #if os(macOS)
                if isDocSelectionActive {
                    docSelectionHeader
                } else if isImageSelectionActive {
                    imageSelectionHeader
                } else {
                    contentRow
                }
                #else
                contentRow
                #endif
            }
            Divider()
            transientSearchPanel
            panelContent
        }
        .onChange(of: artifactVM.mode) { _, _ in
            artifactSearchOpen = false
            artifactSearchDraft = ""
            artifactVM.cloudQuery = ""
            artifactVM.clearSelection()
            artifactVM.exitSelection()
        }
        .onChange(of: documentVM.mode) { old, new in
            docSearchOpen = false
            docSearchDraft = ""
            documentVM.cloudQuery = ""
            documentVM.clearSelection()
            documentVM.exitDocumentSelection()
            if new == .trash {
                Task { await documentVM.loadTrash() }
            } else if old == .trash {
                Task { await documentVM.load() }
            }
        }
        .onChange(of: documentVM.audienceView) { _, _ in
            Task { await documentVM.load() }
        }
        .onChange(of: memoryVM.showCloudMode) { _, show in
            if show {
                Task { await memoryVM.loadTopics() }
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: .sidebarPanelAction)) { note in
            guard let event = note.object as? SidebarPanelActionEvent, event.panel == panel else { return }
            handle(event.action)
        }
        // 关掉 tab 时把跨 tab 的选中状态收干净（原来挂在 browsePanelSelection 变化上）
        .onDisappear {
            docSearchOpen = false
            billSearchOpen = false
            artifactSearchOpen = false
            documentVM.exitDocumentSelection()
            imageVM.exitSelection()
            artifactVM.exitSelection()
        }
        #if os(macOS)
        .confirmationDialog(
            "删除文档",
            isPresented: $isConfirmingBatchDelete,
            titleVisibility: .visible
        ) {
            Button("删除", role: .destructive) {
                Task { await documentVM.deleteSelected() }
            }
            Button("取消", role: .cancel) { }
        } message: {
            Text("确定删除所选 \(documentVM.selectedDocumentIDs.count) 篇？可在回收站找回，7 天后自动清理。")
        }
        #endif
    }

    // MARK: - 模式行（返回/标题已删）

    @ViewBuilder
    private var contentRow: some View {
        HStack(spacing: 8) {
            #if os(macOS)
            if panel == .artifacts {
                Picker("", selection: $artifactVM.mode) {
                    Text("列表").tag(ArtifactBrowseMode.list)
                    Text("词云").tag(ArtifactBrowseMode.cloud)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 120)

                if artifactVM.mode == .cloud {
                    Stepper(value: $artifactVM.minTagCount, in: 1...maxArtifactTagCount) {
                        Text("≥ \(artifactVM.minTagCount) 页")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    .controlSize(.small)
                    .fixedSize()
                }
            }
            #endif

            if panel == .documents {
                Picker("", selection: $documentVM.mode) {
                    Text("列表").tag(DocumentBrowseMode.list)
                    Text("词云").tag(DocumentBrowseMode.cloud)
                    Text("回收站").tag(DocumentBrowseMode.trash)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 180)

                if documentVM.mode != .trash {
                    Button {
                        documentVM.audienceView = documentVM.audienceView.next
                    } label: {
                        Text(documentVM.audienceLabel)
                            .font(.system(size: 12, weight: .medium))
                            .frame(minWidth: 64)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .help("点击切换读者层级：成品 / 底稿 / 全部")
                }

                if documentVM.mode == .cloud {
                    Stepper(value: $documentVM.minTagCount, in: 1...maxTagCount) {
                        Text("≥ \(documentVM.minTagCount) 篇")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    .controlSize(.small)
                    .fixedSize()
                }

                if documentVM.mode == .trash {
                    Button {
                        trashBulkAction = .restoreAll
                    } label: {
                        Image(systemName: "arrow.uturn.backward.circle")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("全部恢复")
                    .disabled(documentVM.trashDocuments.isEmpty)

                    Button {
                        trashBulkAction = .purgeAll
                    } label: {
                        Image(systemName: "trash.slash")
                            .font(.system(size: 12))
                            .foregroundColor(.red)
                    }
                    .buttonStyle(.plain)
                    .help("全部删除（不可恢复）")
                    .disabled(documentVM.trashDocuments.isEmpty)
                }
            }

            #if os(macOS)
            if panel == .memories {
                Picker("", selection: $memoryVM.showCloudMode) {
                    Text("列表").tag(false)
                    Text("词云").tag(true)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 120)

                if memoryVM.showCloudMode {
                    Stepper(value: $memoryVM.minTopicCount, in: 1...maxMemoryTopicCount) {
                        Text("≥ \(memoryVM.minTopicCount) 条")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    .controlSize(.small)
                    .fixedSize()
                } else {
                    Button {
                        memoryVM.showSourceBadge.toggle()
                    } label: {
                        Image(systemName: memoryVM.showSourceBadge ? "tag" : "tag.slash")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help(memoryVM.showSourceBadge ? "隐藏来源标签" : "显示来源标签")
                }
            }
            #endif

            Spacer(minLength: 0)

            if panel == .documents && documentVM.mode != .trash {
                headerSearchToggle(
                    active: docSearchActive,
                    help: documentVM.mode == .cloud ? "搜索并高亮标签" : "搜索文档"
                ) { toggleDocSearch() }
            } else if panel == .bills {
                headerSearchToggle(
                    active: billSearchOpen || !billVM.keyword.isEmpty,
                    help: "搜索账单"
                ) { toggleBillSearch() }
            }
            #if os(macOS)
            if panel == .artifacts {
                headerSearchToggle(
                    active: artifactSearchActive,
                    help: artifactVM.mode == .cloud ? "搜索并高亮标签" : "搜索页面"
                ) { toggleArtifactSearch() }
            }
            #endif
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        #if os(macOS)
        .confirmationDialog(
            trashBulkAction == .purgeAll ? "清空回收站" : "全部恢复",
            isPresented: Binding(
                get: { trashBulkAction != nil },
                set: { if !$0 { trashBulkAction = nil } }
            ),
            titleVisibility: .visible
        ) {
            if trashBulkAction == .purgeAll {
                Button("全部删除", role: .destructive) {
                    Task { await documentVM.purgeAll() }
                }
            } else {
                Button("全部恢复") {
                    Task { await documentVM.restoreAll() }
                }
            }
            Button("取消", role: .cancel) { }
        } message: {
            Text(trashBulkAction == .purgeAll
                 ? "将永久删除回收站中的全部文档，无法恢复。"
                 : "将把回收站中的全部文档恢复到文档列表。")
        }
        #endif
    }

    // MARK: - 搜索

    private func toggleDocSearch() {
        guard !docSearchOpen else { docSearchOpen = false; return }
        billSearchOpen = false
        docSearchDraft = documentVM.mode == .cloud ? documentVM.cloudQuery : documentVM.keyword
        docSearchOpen = true
    }

    private var docSearchActive: Bool {
        guard !docSearchOpen else { return true }
        return documentVM.mode == .cloud ? !documentVM.cloudQuery.isEmpty : !documentVM.keyword.isEmpty
    }

    private var docSearchCaption: String {
        let names = DocumentSearchField.allCases
            .filter { documentVM.searchFields.contains($0) }
            .map(\.label)
        return "按 \(names.joined(separator: " / ")) 检索，点输入框内按钮切换字段"
    }

    private func toggleBillSearch() {
        guard !billSearchOpen else { billSearchOpen = false; return }
        docSearchOpen = false
        billSearchDraft = billVM.keyword
        billSearchOpen = true
    }

    private func toggleArtifactSearch() {
        guard !artifactSearchOpen else { artifactSearchOpen = false; return }
        docSearchOpen = false
        billSearchOpen = false
        artifactSearchDraft = artifactVM.mode == .cloud ? artifactVM.cloudQuery : artifactVM.keyword
        artifactSearchOpen = true
    }

    private var artifactSearchActive: Bool {
        guard !artifactSearchOpen else { return true }
        return artifactVM.mode == .cloud ? !artifactVM.cloudQuery.isEmpty : !artifactVM.keyword.isEmpty
    }

    @ViewBuilder
    private var transientSearchPanel: some View {
        switch panel {
        case .documents where docSearchOpen && documentVM.mode == .cloud:
            TransientSearchPanel(
                placeholder: "搜索标签并高亮",
                caption: "在词云中高亮匹配的标签（纯本地）",
                draft: $docSearchDraft,
                live: true,
                onSearch: { documentVM.cloudQuery = $0 },
                onClear: {
                    docSearchDraft = ""
                    documentVM.cloudQuery = ""
                }
            )
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        case .documents where docSearchOpen:
            TransientSearchPanel(
                placeholder: "搜索标题/标签/正文",
                caption: docSearchCaption,
                draft: $docSearchDraft,
                fields: $documentVM.searchFields,
                onSearch: { kw in
                    Task { await documentVM.search(kw) }
                    docSearchOpen = false
                },
                onClear: {
                    docSearchDraft = ""
                    Task { await documentVM.search("") }
                    docSearchOpen = false
                }
            )
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        case .artifacts where artifactSearchOpen && artifactVM.mode == .cloud:
            TransientSearchPanel(
                placeholder: "搜索标签并高亮",
                caption: "在词云中高亮匹配的标签（纯本地）",
                draft: $artifactSearchDraft,
                live: true,
                onSearch: { artifactVM.cloudQuery = $0 },
                onClear: {
                    artifactSearchDraft = ""
                    artifactVM.cloudQuery = ""
                }
            )
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        case .artifacts where artifactSearchOpen:
            TransientSearchPanel(
                placeholder: "搜索标题或摘要",
                caption: "按标题/摘要检索，与标签筛选叠加生效",
                draft: $artifactSearchDraft,
                onSearch: { kw in
                    Task { await artifactVM.search(kw) }
                    artifactSearchOpen = false
                },
                onClear: {
                    artifactSearchDraft = ""
                    Task { await artifactVM.search("") }
                    artifactSearchOpen = false
                }
            )
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        case .bills where billSearchOpen:
            TransientSearchPanel(
                placeholder: "搜索项目或备注",
                caption: "按项目/备注检索，与日期/分类筛选叠加生效",
                draft: $billSearchDraft,
                onSearch: { kw in
                    Task { await billVM.search(kw) }
                    billSearchOpen = false
                },
                onClear: {
                    billSearchDraft = ""
                    Task { await billVM.search("") }
                    billSearchOpen = false
                }
            )
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        default:
            EmptyView()
        }
    }

    // MARK: - 多选态

    private var isArtifactSelectionActive: Bool {
        panel == .artifacts && artifactVM.isSelecting
    }

    #if os(macOS)
    private var isDocSelectionActive: Bool {
        documentVM.isSelectingDocuments && documentVM.mode == .list
    }

    private var isImageSelectionActive: Bool {
        imageVM.isSelecting && panel == .images
    }

    private var imageSelectionHeader: some View {
        SelectionToolbar(
            count: imageVM.selectedIDs.count,
            unit: "张",
            emptyText: "选择图片",
            cancelHelp: "退出多选（Esc）",
            onCancel: { imageVM.exitSelection() }
        ) {
            Button {
                imageVM.selectAll()
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 12))
                    Text("全选")
                        .font(.system(size: 12))
                }
                .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .disabled(imageVM.items.isEmpty)
            .help("全选已加载的图片")

            Button {
                isConfirmingImageBatchDelete = true
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "trash")
                        .font(.system(size: 12))
                    Text("删除")
                        .font(.system(size: 12, weight: .semibold))
                }
                .foregroundColor(.red)
            }
            .buttonStyle(.plain)
            .disabled(imageVM.selectedIDs.isEmpty || imageVM.isDeleting)
            .help("删除所选图片")
        }
    }

    private var docSelectionHeader: some View {
        SelectionToolbar(
            count: documentVM.selectedDocumentIDs.count,
            unit: "篇",
            emptyText: "选择文档",
            cancelHelp: "退出多选（Esc）",
            onCancel: { documentVM.exitDocumentSelection() }
        ) {
            Button {
                isConfirmingBatchDelete = true
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "trash")
                        .font(.system(size: 12))
                    Text("删除")
                        .font(.system(size: 12, weight: .semibold))
                }
                .foregroundColor(.red)
            }
            .buttonStyle(.plain)
            .disabled(documentVM.selectedDocumentIDs.isEmpty)
            .help("删除所选文档")

            Button {
                downloadSelectedDocuments()
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "arrow.down.circle")
                        .font(.system(size: 12))
                    Text("下载")
                        .font(.system(size: 12, weight: .semibold))
                }
            }
            .buttonStyle(.borderedProminent)
            .tint(Color("appAccent"))
            .controlSize(.small)
            .disabled(documentVM.selectedDocumentIDs.isEmpty)
            .help("下载所选文档为 .md")
        }
    }

    private func downloadSelectedDocuments() {
        let docs = documentVM.documents.filter { documentVM.selectedDocumentIDs.contains($0.id) }
        guard !docs.isEmpty else { return }
        let panel = NSOpenPanel()
        panel.title = "选择下载文件夹"
        panel.prompt = "下载"
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true
        panel.begin { response in
            guard response == .OK, let dir = panel.url else { return }
            Task { @MainActor in
                var written = 0
                for doc in docs {
                    let url = Self.uniqueFileURL(title: doc.title, fallback: doc.id, directory: dir)
                    do {
                        try doc.content.write(to: url, atomically: true, encoding: .utf8)
                        written += 1
                    } catch {
                        LogBuffer.shared.error("document batch download failed (\(doc.title)): \(error.localizedDescription)")
                    }
                }
                ToastCenter.shared.show(written == docs.count
                    ? "已下载 \(written) 篇"
                    : "已下载 \(written)/\(docs.count) 篇，其余失败")
                documentVM.exitDocumentSelection()
            }
        }
    }

    private static func uniqueFileURL(title: String, fallback: String, directory: URL) -> URL {
        let base = (title.isEmpty ? fallback : title)
            .replacingOccurrences(of: "/", with: "-")
            .replacingOccurrences(of: ":", with: "-")
        var candidate = directory.appendingPathComponent(base + ".md")
        var n = 2
        while FileManager.default.fileExists(atPath: candidate.path) {
            candidate = directory.appendingPathComponent("\(base) (\(n)).md")
            n += 1
        }
        return candidate
    }
    #endif

    private var artifactSelectionHeader: some View {
        SelectionToolbar(
            count: artifactVM.selectedIDs.count,
            unit: "页",
            emptyText: "选择页面",
            cancelHelp: "退出多选（Esc）",
            onCancel: { artifactVM.exitSelection() }
        ) {
            Button {
                artifactVM.selectAll()
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 12))
                    Text("全选")
                        .font(.system(size: 12))
                }
                .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .disabled(artifactVM.items.isEmpty)
            .help("全选已加载的页面")

            Button {
                isConfirmingArtifactBatchDelete = true
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: "trash")
                        .font(.system(size: 12))
                    Text("删除")
                        .font(.system(size: 12, weight: .semibold))
                }
                .foregroundColor(.red)
            }
            .buttonStyle(.plain)
            .disabled(artifactVM.selectedIDs.isEmpty || artifactVM.isDeleting)
            .help("删除所选页面")
        }
    }

    private var maxTagCount: Int {
        max(1, documentVM.tags.first?.count ?? 1)
    }

    private var maxArtifactTagCount: Int {
        max(artifactVM.minTagCount, artifactVM.tags.first?.count ?? 1)
    }

    private var maxMemoryTopicCount: Int {
        max(memoryVM.minTopicCount, memoryVM.cloudTopics.first?.count ?? 1)
    }

    private func headerSearchToggle(active: Bool, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 12))
                .foregroundColor(active ? Color("appAccent") : .secondary)
        }
        .buttonStyle(.plain)
        .help(help)
    }

    // MARK: - 面板内容

    @ViewBuilder
    private var panelContent: some View {
        switch panel {
        case .bills:
            BillPage(viewModel: billVM, baseURL: baseURL, token: token)
        #if os(macOS)
        case .books:
            BookshelfView(viewModel: bookVM, onOpen: onOpenBook)
        #endif
        case .reminders:
            ReminderPageView(
                items: homeVM.reminders,
                isLoading: homeVM.isLoading,
                errorMessage: homeVM.remindersFailed && homeVM.reminders.isEmpty
                    ? "加载失败，点刷新重试" : nil,
                onRetry: { Task { await homeVM.refresh(baseURL: baseURL, token: token, force: true) } }
            )
            .task { await homeVM.refresh(baseURL: baseURL, token: token) }
        case .memories:
            MemoryPage(viewModel: memoryVM)
        case .artifacts:
            ArtifactPage(viewModel: artifactVM)
                .confirmationDialog(
                    "删除页面",
                    isPresented: $isConfirmingArtifactBatchDelete,
                    titleVisibility: .visible
                ) {
                    Button("删除", role: .destructive) {
                        Task { await artifactVM.deleteSelected() }
                    }
                    Button("取消", role: .cancel) { }
                } message: {
                    Text("选中的 \(artifactVM.selectedIDs.count) 页将被删除，不能恢复；已分享的链接会立即失效。")
                }
        case .documents:
            DocumentPage(viewModel: documentVM, onOpen: onOpenDocument)
        case .images:
            ImagePage(viewModel: imageVM, baseURL: baseURL, token: token)
                .confirmationDialog(
                    "删除图片",
                    isPresented: $isConfirmingImageBatchDelete,
                    titleVisibility: .visible
                ) {
                    Button("删除", role: .destructive) {
                        Task { await imageVM.deleteSelected() }
                    }
                    Button("取消", role: .cancel) { }
                } message: {
                    Text(imageBatchDeleteMessage)
                }
        case .events:
            EventPage(viewModel: eventVM)
        case .agents:
            AgentPage(viewModel: agentVM)
        case .dashboard:
            if let url = dashboardURL {
                XHWebViewPanel(
                    model: dashboardWebModel,
                    url: url,
                    configuration: dashboardConfig,
                    allowedOrigins: dashboardAllowedOrigins
                )
            } else {
                Text("无法构建监控看板地址")
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
    }

    // MARK: - 顶栏动作

    private func handle(_ action: SidebarPanelAction) {
        switch action {
        case .refresh:
            switch panel {
            case .bills: Task { await billVM.load() }
            #if os(macOS)
            case .books: Task { await bookVM.load() }
            #endif
            case .reminders: Task { await homeVM.refresh(baseURL: baseURL, token: token, force: true) }
            case .memories: Task { await memoryVM.load() }
            case .artifacts: Task { await artifactVM.load() }
            case .documents: Task { await documentVM.loadCurrentMode() }
            case .images: Task { await imageVM.load() }
            case .events: Task { await eventVM.load() }
            case .agents: Task { await agentVM.load() }
            case .dashboard: dashboardBust = Int(Date().timeIntervalSince1970)
            }
        case .multiselect:
            switch panel {
            case .documents: documentVM.enterDocumentSelection()
            case .artifacts: artifactVM.enterSelection()
            case .images: imageVM.enterSelection()
            default: break
            }
        case .exportCSV:
            exportCSV()
        case .importBook:
            #if os(macOS)
            bookVM.presentImportPanel()
            #endif
        }
    }

    private func exportCSV() {
        #if os(macOS)
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.commaSeparatedText]
        panel.nameFieldStringValue = "bills.csv"
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task {
                guard let csv = await billVM.exportCSV(baseURL: baseURL, token: token) else { return }
                do {
                    try csv.write(to: url, atomically: true, encoding: .utf8)
                } catch {
                    LogBuffer.shared.error("exportCSV write: \(error)")
                    ToastCenter.shared.show("保存失败：\(error.localizedDescription)")
                }
            }
        }
        #endif
    }

    private var imageBatchDeleteMessage: String {
        let n = imageVM.selectedIDs.count
        let risky = imageVM.items.contains {
            imageVM.selectedIDs.contains($0.id) && ($0.referenced || $0.source != "chat")
        }
        if risky {
            return "确定删除所选 \(n) 张？其中含已被消息引用、或来自文档/网页/本地等非聊天来源的图，删除后对应位置会显示占位或裂图。"
        }
        return "确定删除所选 \(n) 张？"
    }
}

/// 文档/账单共用的瞬时搜索功能区（页头🔍点开，操作后收起；不构成常驻工具条）。
private struct TransientSearchPanel: View {
    let placeholder: String
    let caption: String?
    @Binding var draft: String
    var live: Bool = false
    var fields: Binding<Set<DocumentSearchField>>? = nil
    let onSearch: (String) -> Void
    let onClear: () -> Void

    private var trimmed: String {
        draft.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                if let fields {
                    HStack(spacing: 4) {
                        Image(systemName: "magnifyingglass")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)

                        TextField(placeholder, text: $draft)
                            .textFieldStyle(.plain)
                            .font(.system(size: 12))
                            .onSubmit(runSearch)

                        fieldToggles(fields)
                    }
                    .padding(.horizontal, 6)
                    .padding(.vertical, 3)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Color.secondary.opacity(0.08)))
                    .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.secondary.opacity(0.25)))
                } else {
                    Image(systemName: "magnifyingglass")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)

                    TextField(placeholder, text: $draft)
                        .textFieldStyle(.roundedBorder)
                        .font(.system(size: 12))
                        .onSubmit(runSearch)
                }

                if !trimmed.isEmpty {
                    Button {
                        onClear()
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("清空搜索")
                }

                if !live {
                    Button {
                        runSearch()
                    } label: {
                        Text("搜索")
                            .font(.system(size: 11))
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Color("appAccent"))
                    .controlSize(.small)
                    .disabled(trimmed.isEmpty)
                }
            }

            if let caption {
                Text(caption)
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 10))
        .onChange(of: draft) { _, _ in
            if live { onSearch(trimmed) }
        }
    }

    private func fieldToggles(_ fields: Binding<Set<DocumentSearchField>>) -> some View {
        HStack(spacing: 2) {
            ForEach(DocumentSearchField.allCases, id: \.self) { field in
                let on = fields.wrappedValue.contains(field)
                Button {
                    toggle(field, in: fields)
                } label: {
                    Text(field.label)
                        .font(.system(size: 9, weight: .medium))
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(Capsule().fill(on ? Color("appAccent").opacity(0.20) : Color.secondary.opacity(0.10)))
                        .foregroundColor(on ? Color("appAccent") : Color.secondary)
                }
                .buttonStyle(.plain)
                .help("检索字段：\(field.label)（可多选，至少保留一个）")
            }
        }
        .fixedSize()
    }

    private func toggle(_ field: DocumentSearchField, in fields: Binding<Set<DocumentSearchField>>) {
        var set = fields.wrappedValue
        if set.contains(field) {
            guard set.count > 1 else { return }
            set.remove(field)
        } else {
            set.insert(field)
        }
        fields.wrappedValue = set
    }

    private func runSearch() {
        let kw = trimmed
        guard !kw.isEmpty else { return }
        onSearch(kw)
    }
}
