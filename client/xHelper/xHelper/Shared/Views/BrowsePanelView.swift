import SwiftUI
import WebKit
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

/// 右侧栏浏览面板类型
enum BrowsePanel: String, Identifiable, CaseIterable {
    case bills = "账单"
    case memories = "记忆"
    case notes = "笔记"
    case documents = "文档"
    case images = "图片"
    case themes = "主题"
    case dashboard = "监控看版"
    case agents = "智能体"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .bills:     return "yensign.circle"
        case .memories:  return "brain.head.profile"
        case .notes:     return "note.text"
        case .documents: return "doc.text"
        case .images:    return "photo.on.rectangle.angled"
        case .themes:    return "tag"
        case .dashboard: return "chart.bar.xaxis"
        case .agents:    return "cpu"
        }
    }

    var help: String {
        switch self {
        case .bills:     return "查看账单记录与统计"
        case .memories:  return "长期记忆与语义搜索"
        case .notes:     return "笔记与标签检索"
        case .documents: return "Markdown 文档与版本"
        case .images:    return "图片库：预览与清理已上传的图"
        case .themes:    return "主题状态追踪"
        case .dashboard: return "前端监控看板（成本统计等）"
        case .agents:    return "自定义智能体管理"
        }
    }
}

/// 回收站批量操作（页头按钮 → 二次确认）
private enum TrashBulkAction {
    case restoreAll
    case purgeAll
}

/// 右侧栏浏览面板：网格入口（含打开浏览器）+ 浏览页面切换
struct BrowsePanelView: View {
    @ObservedObject var manager: RightSidebarManager
    @ObservedObject var billVM: BillViewModel
    @ObservedObject var memoryVM: MemoryViewModel
    @ObservedObject var noteVM: NoteViewModel
    @ObservedObject var documentVM: DocumentViewModel
    @ObservedObject var themeVM: ThemeViewModel
    @ObservedObject var agentVM: AgentViewModel
    @ObservedObject var imageVM: ImageViewModel
    let baseURL: String
    let token: String

    /// 监控看板 WebView 共享状态（header 刷新按钮 + WebView 共用）
    @StateObject private var dashboardWebModel = XHWebViewModel()

    /// 缓存击穿戳：每次打开面板/点刷新自增，URL 带 ?t= 强制回源拉新 index.html。
    /// 服务器静态资源无 Cache-Control，WKWebView 启发式缓存会让 reloadFromOrigin 命中旧文档，
    /// 只有换 URL 才能绕过。
    @State private var dashboardBust = Int(Date().timeIntervalSince1970)

    /// 文档/账单页头搜索图标展开态 + 搜索框草稿（切页复位，同一时刻至多开一个）。
    @State private var docSearchOpen = false
    @State private var billSearchOpen = false
    @State private var docSearchDraft = ""
    @State private var billSearchDraft = ""
    /// 多选删除二次确认。
    @State private var isConfirmingBatchDelete = false
    /// 回收站批量操作二次确认（全部恢复 / 全部删除）。
    @State private var trashBulkAction: TrashBulkAction?

    /// 监控看板前端地址：统一从 `DeviceManager.webBaseURL` 推导——
    /// 本地模式 = 内嵌 Go 托管的前端（同源 127.0.0.1:<LocalPaths.backendPort>）；非本地 Debug = Vite 5173；
    /// 线上 Release = helper.xhanx.cn。
    private var dashboardURL: URL? {
        URL(string: DeviceManager.webBaseURL + "/admin?t=\(dashboardBust)")
    }

    /// 监控看板 WebView 配置：注入 XH_BRIDGE 全局对象（含 token 免登录）+ 外链转发系统浏览器
    private var dashboardConfig: WKWebViewConfiguration {
        let externalLink = XHWebViewBridge.ScriptMessageHandler { body in
            // 仅接受 dict 形式 {url: "..."}；scheme 白名单 http/https + 长度上限
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

    /// 导航白名单：仅允许前端所在同源（含本地内嵌 127.0.0.1:<LocalPaths.backendPort>），其余一律静默拦截
    private var dashboardAllowedOrigins: [XHWebViewOrigin] {
        XHWebViewOrigin.frontendOrigins(webBaseURL: DeviceManager.webBaseURL)
    }

    /// 根据网格可用宽度自适应列数：宽时 3 列（9 格 3 行），窄时 2 列（9 格 5 行，靠滚动兜住）。
    /// 宽态：先维持 110 的格子宽、把剩余空间给间距；空间不足时间距压到最小、格子随之收缩；
    /// 空间富余时间距封顶、格子保持 110。
    private func adaptiveColumns(width: CGFloat) -> (columns: [GridItem], spacing: CGFloat) {
        if width >= 480 {
            let maxTile: CGFloat = 110
            let minTile: CGFloat = 84
            let maxSpacing: CGFloat = 20
            let minSpacing: CGFloat = 6

            var spacing = (width - 3 * maxTile) / 2
            var tile = maxTile
            if spacing < minSpacing {
                spacing = minSpacing
                tile = max(minTile, (width - 2 * minSpacing) / 3)
            } else if spacing > maxSpacing {
                spacing = maxSpacing
            }
            let columns = Array(repeating: GridItem(.fixed(tile), spacing: spacing), count: 3)
            return (columns, spacing)
        } else {
            let maxTile: CGFloat = 160
            let minTile: CGFloat = 90
            let maxSpacing: CGFloat = 20
            let minSpacing: CGFloat = 8

            var spacing = width - 2 * maxTile
            var tile = maxTile
            if spacing < minSpacing {
                spacing = minSpacing
                tile = max(minTile, (width - minSpacing) / 2)
            } else if spacing > maxSpacing {
                spacing = maxSpacing
            }
            let columns = Array(repeating: GridItem(.fixed(tile), spacing: spacing), count: 2)
            return (columns, spacing)
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            if let panel = manager.browsePanelSelection {
                if panel == .dashboard {
                    // 监控看板自带 XHPanel header（含刷新按钮）
                    panelContent(panel)
                } else {
                    #if os(macOS)
                    if isDocSelectionActive {
                        docSelectionHeader
                    } else {
                        panelHeader(panel)
                    }
                    #else
                    panelHeader(panel)
                    #endif
                    Divider()
                    transientSearchPanel(panel)
                    panelContent(panel)
                }
            } else {
                grid
            }
        }
        .onChange(of: manager.browsePanelSelection) { _, newValue in
            if newValue == nil {
                docSearchOpen = false
                billSearchOpen = false
            }
            // 离开文档面板即退出多选（选择工具条只在文档列表前台时有效）
            documentVM.exitDocumentSelection()
        }
        .onChange(of: documentVM.mode) { old, new in
            // 切模式即收起文档搜索框；离开词云顺手清掉本地高亮词与多选，避免切回来还留着
            docSearchOpen = false
            docSearchDraft = ""
            documentVM.cloudQuery = ""
            documentVM.clearSelection()
            documentVM.exitDocumentSelection()
            // 进入/离开回收站时拉对应列表（selectTag/clearFilter 自带的 load 不会走到这里）
            if new == .trash {
                Task { await documentVM.loadTrash() }
            } else if old == .trash {
                Task { await documentVM.load() }
            }
        }
        .onChange(of: memoryVM.showCloudMode) { _, show in
            // 进词云拉 topic（聚合轻量）；面板首次打开时列表分支由 listPage 的 .task 负责
            if show {
                Task { await memoryVM.loadTopics() }
            }
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

    // MARK: - 页头搜索（文档/账单瞬时功能区）

    private func toggleDocSearch() {
        guard !docSearchOpen else { docSearchOpen = false; return }
        billSearchOpen = false
        // 词云模式草稿来自本地高亮词，列表模式来自后端关键词
        docSearchDraft = documentVM.mode == .cloud ? documentVM.cloudQuery : documentVM.keyword
        docSearchOpen = true
    }

    /// 文档页头搜索激活态：词云模式看 cloudQuery，列表模式看后端 keyword。
    private var docSearchActive: Bool {
        guard !docSearchOpen else { return true }
        return documentVM.mode == .cloud ? !documentVM.cloudQuery.isEmpty : !documentVM.keyword.isEmpty
    }

    /// 文档搜索说明：随所选检索字段变化。
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

    @ViewBuilder
    private func transientSearchPanel(_ panel: BrowsePanel) -> some View {
        switch panel {
        case .documents where docSearchOpen && documentVM.mode == .cloud:
            // 词云：本地实时高亮，面板不收起
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

    // MARK: - 网格

    private var grid: some View {
        // 10 格：宽 5 列 × 2 行、窄 2 列 × 5 行。窄态高度不够时靠 ScrollView 兜住，
        // 内容比视口矮时用 minHeight 撑满以保持垂直居中。
        GeometryReader { geo in
            let (columns, spacing) = adaptiveColumns(width: geo.size.width - 48)
            ScrollView {
                LazyVGrid(columns: columns, spacing: spacing) {
                    browserTile
                    ForEach(BrowsePanel.allCases) { panel in
                        tile(icon: panel.icon, title: panel.rawValue, help: panel.help) {
                            manager.browsePanelSelection = panel
                        }
                    }
                }
                .padding(.horizontal, 24)
                .padding(.vertical, 20)
                .frame(maxWidth: .infinity, minHeight: geo.size.height)
            }
        }
    }

    private var browserTile: some View {
        tile(icon: "globe", title: "打开浏览器", help: "打开浏览器标签页，可先登录账号") {
            BrowserToolProvider.shared.openBlankTab()
        }
    }

    private func tile(icon: String, title: String, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: 8) {
                Image(systemName: icon)
                    .font(.system(size: 26))
                    .foregroundColor(.accentColor)
                    .frame(width: 52, height: 52)
                    .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 12))
                Text(title)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(.primary)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(help)
    }

    // MARK: - 浏览页面

    private func panelHeader(_ panel: BrowsePanel) -> some View {
        HStack(spacing: 8) {
            Button {
                manager.browsePanelSelection = nil
            } label: {
                HStack(spacing: 3) {
                    Image(systemName: "chevron.left")
                        .font(.system(size: 11, weight: .semibold))
                    Text("返回")
                        .font(.system(size: 12))
                }
                .foregroundColor(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("返回网格")

            Divider()
                .frame(height: 14)

            Text(panel.rawValue)
                .font(.system(size: 13, weight: .semibold))

            Spacer()

            if panel == .documents && documentVM.openDocument == nil {
                Picker("", selection: $documentVM.mode) {
                    Text("列表").tag(DocumentBrowseMode.list)
                    Text("词云").tag(DocumentBrowseMode.cloud)
                    Text("回收站").tag(DocumentBrowseMode.trash)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 180)

                if documentVM.mode == .cloud {
                    Stepper(value: $documentVM.minTagCount, in: 1...maxTagCount) {
                        Text("≥ \(documentVM.minTagCount) 篇")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    .controlSize(.small)
                    .fixedSize()
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
                }
            }

            if panel == .documents && documentVM.openDocument == nil && documentVM.mode == .list {
                Button {
                    documentVM.enterDocumentSelection()
                } label: {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("多选下载")
            }

            if panel == .documents && documentVM.openDocument == nil && documentVM.mode == .trash {
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
            #endif

            Button {
                Task { await refresh(panel) }
            } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 12))
                    .foregroundColor(isLoading(panel) ? Color("appAccent") : .secondary)
            }
            .buttonStyle(.plain)
            .help("刷新")

            if panel == .bills {
                Button {
                    exportCSV()
                } label: {
                    Image(systemName: "tray.and.arrow.up")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("导出 CSV")
            }

            if panel == .documents && documentVM.openDocument == nil && documentVM.mode != .trash {
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

    #if os(macOS)
    /// 文档列表多选态是否生效（选择工具条取代常规页头）。
    private var isDocSelectionActive: Bool {
        documentVM.isSelectingDocuments && documentVM.mode == .list && documentVM.openDocument == nil
    }

    /// 多选工具条：取消（左）/ 已选篇数 / 删除 + 下载（右）。
    private var docSelectionHeader: some View {
        HStack(spacing: 8) {
            Button {
                documentVM.exitDocumentSelection()
            } label: {
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
            .help("退出多选（Esc）")

            Divider()
                .frame(height: 14)

            Text(documentVM.selectedDocumentIDs.isEmpty
                 ? "选择文档"
                 : "已选 \(documentVM.selectedDocumentIDs.count) 篇")
                .font(.system(size: 13, weight: .semibold))

            Spacer()

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
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    /// 批量下载所选文档为 .md 到用户选择的文件夹。
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

    /// 文件名安全化 + 目录内去重：`<标题>.md`，重名追加 ` (2)`、` (3)`…
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

    /// 文档词云的最少篇数上限（标签已按频次降序）
    private var maxTagCount: Int {
        max(1, documentVM.tags.first?.count ?? 1)
    }

    /// 记忆词云的最少条数上限（topic 已按频次降序；不低过当前值，避免 Stepper 范围越界）
    private var maxMemoryTopicCount: Int {
        max(memoryVM.minTopicCount, memoryVM.cloudTopics.first?.count ?? 1)
    }

    /// 页头搜索图标：面板展开或有关键词生效时 accent 高亮（对齐账单筛选激活态）。
    private func headerSearchToggle(active: Bool, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 12))
                .foregroundColor(active ? Color("appAccent") : .secondary)
        }
        .buttonStyle(.plain)
        .help(help)
    }

    /// 当前面板是否在加载：详情页在前台时文档面板按详情加载态计。
    private func isLoading(_ panel: BrowsePanel) -> Bool {
        switch panel {
        case .bills:     return billVM.isLoading
        case .memories:  return memoryVM.isLoading
        case .notes:     return noteVM.isLoading
        case .documents:
            if documentVM.openDocument != nil { return documentVM.isDetailLoading }
            return documentVM.mode == .trash ? documentVM.isTrashLoading : documentVM.isLoading
        case .images:    return imageVM.isLoading
        case .themes:    return themeVM.isLoading
        case .agents:    return agentVM.isLoading
        case .dashboard: return false
        }
    }

    /// 刷新当前正在看的内容：文档详情在前台时刷新单篇，否则刷新列表。
    private func refresh(_ panel: BrowsePanel) async {
        if panel == .documents && documentVM.openDocument != nil {
            await documentVM.reloadOpenDocument()
        } else {
            await reload(panel)
        }
    }

    private func reload(_ panel: BrowsePanel) async {
        switch panel {
        case .bills:     await billVM.load()
        case .memories:  await memoryVM.load()
        case .notes:     await noteVM.load()
        case .documents: await documentVM.loadCurrentMode()
        case .images:    await imageVM.load()
        case .themes:    await themeVM.load()
        case .agents:    await agentVM.load()
        case .dashboard: break
        }
    }

    @ViewBuilder
    private func panelContent(_ panel: BrowsePanel) -> some View {
        switch panel {
        case .bills:
            BillPage(viewModel: billVM, baseURL: baseURL, token: token)
        case .memories:
            MemoryPage(viewModel: memoryVM)
        case .notes:
            NotePage(viewModel: noteVM)
        case .documents:
            DocumentPage(viewModel: documentVM)
        case .images:
            ImagePage(viewModel: imageVM, baseURL: baseURL, token: token)
        case .themes:
            ThemePage(viewModel: themeVM)
        case .agents:
            AgentPage(viewModel: agentVM)
        case .dashboard:
            if let url = dashboardURL {
                XHPanel(
                    title: panel.rawValue,
                    onBack: { manager.browsePanelSelection = nil },
                    trailingButtons: [
                        XHPanel.XHPanelButton(
                            icon: "arrow.clockwise",
                            help: "刷新",
                            action: { dashboardBust = Int(Date().timeIntervalSince1970) }
                        )
                    ]
                ) {
                    XHWebViewPanel(
                        model: dashboardWebModel,
                        url: url,
                        configuration: dashboardConfig,
                        allowedOrigins: dashboardAllowedOrigins
                    )
                }
            } else {
                Text("无法构建监控看板地址")
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
    }

    // MARK: - Export

    private func exportCSV() {
        #if os(macOS)
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.commaSeparatedText]
        panel.nameFieldStringValue = "bills.csv"
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task {
                guard let csv = await billVM.exportCSV(baseURL: baseURL, token: token) else { return }
                try? csv.write(to: url, atomically: true, encoding: .utf8)
            }
        }
        #endif
    }
}

/// 文档/账单共用的瞬时搜索功能区（页头🔍点开，操作后收起；不构成常驻工具条）。
private struct TransientSearchPanel: View {
    let placeholder: String
    let caption: String?
    @Binding var draft: String
    /// 实时模式：不显示「搜索」按钮，输入即回调（词云本地高亮用）。是否收起由调用方决定。
    var live: Bool = false
    /// 检索目标多选（文档用）；nil 时不渲染三按钮（账单复用本组件）。
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
                    // 自绘字段容器：SwiftUI TextField 不能内嵌 trailing，只能仿 .roundedBorder 外观
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

    /// 输入框内右对齐的三个字段开关（title / tag / content）。
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

    /// 至少保留一个字段：关掉最后一个亮着的按钮是 no-op。
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
