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
        case .themes:    return "主题状态追踪"
        case .dashboard: return "前端监控看板（成本统计等）"
        case .agents:    return "自定义智能体管理"
        }
    }
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

    /// 监控看板前端地址：DEBUG 用 Vite dev server，RELEASE 用同源后端（nginx 已 serve）
    private var dashboardURL: URL? {
        let suffix = "?t=\(dashboardBust)"
        #if DEBUG
        return URL(string: "http://localhost:5173/admin" + suffix)
        #else
        return URL(string: baseURL + "/admin" + suffix)
        #endif
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

    /// 导航白名单：仅允许前端所在同源，其余一律静默拦截
    private var dashboardAllowedOrigins: [XHWebViewOrigin] {
        [
            XHWebViewOrigin(scheme: "http", host: "localhost", port: 5173),      // DEBUG Vite dev server
            XHWebViewOrigin(scheme: "https", host: "helper.xhanx.cn", port: nil), // RELEASE 远程
        ]
    }

    /// 根据网格可用宽度自适应列数：宽时 4 列（8 格 2 行），窄时 2 列（8 格 4 行）。
    /// 宽态：先维持 110 的格子宽、把剩余空间给间距；空间不足时间距压到最小、格子随之收缩；
    /// 空间富余时间距封顶、格子保持 110。
    private func adaptiveColumns(width: CGFloat) -> (columns: [GridItem], spacing: CGFloat) {
        if width >= 480 {
            let maxTile: CGFloat = 110
            let minTile: CGFloat = 84
            let maxSpacing: CGFloat = 20
            let minSpacing: CGFloat = 6

            var spacing = (width - 4 * maxTile) / 3
            var tile = maxTile
            if spacing < minSpacing {
                spacing = minSpacing
                tile = max(minTile, (width - 3 * minSpacing) / 4)
            } else if spacing > maxSpacing {
                spacing = maxSpacing
            }
            let columns = Array(repeating: GridItem(.fixed(tile), spacing: spacing), count: 4)
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
                    panelHeader(panel)
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
        }
    }

    // MARK: - 页头搜索（文档/账单瞬时功能区）

    private func toggleDocSearch() {
        guard !docSearchOpen else { docSearchOpen = false; return }
        billSearchOpen = false
        docSearchDraft = documentVM.keyword
        docSearchOpen = true
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
        case .documents where docSearchOpen:
            TransientSearchPanel(
                placeholder: "搜索标题或内容",
                caption: "按标题/正文检索",
                draft: $docSearchDraft,
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
        // 固定 8 格无需滚动，垂直居中展示；间距/格子随可用宽度自适应收缩
        GeometryReader { geo in
            let (columns, spacing) = adaptiveColumns(width: geo.size.width - 48)
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
            .frame(maxWidth: .infinity, maxHeight: .infinity)
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
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 120)

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

            if panel == .documents {
                headerSearchToggle(
                    active: docSearchOpen || !documentVM.keyword.isEmpty,
                    help: "搜索文档"
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
    }

    /// 文档词云的最少篇数上限（标签已按频次降序）
    private var maxTagCount: Int {
        max(1, documentVM.tags.first?.count ?? 1)
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
        case .documents: return documentVM.openDocument != nil ? documentVM.isDetailLoading : documentVM.isLoading
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
        case .documents: await documentVM.load()
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
    let onSearch: (String) -> Void
    let onClear: () -> Void

    private var trimmed: String {
        draft.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)

                TextField(placeholder, text: $draft)
                    .textFieldStyle(.roundedBorder)
                    .font(.system(size: 12))
                    .onSubmit(runSearch)

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

            if let caption {
                Text(caption)
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 10))
    }

    private func runSearch() {
        let kw = trimmed
        guard !kw.isEmpty else { return }
        onSearch(kw)
    }
}
