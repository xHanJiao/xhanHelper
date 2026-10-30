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
    case artifacts = "灵阅"
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
        case .artifacts: return "doc.richtext"
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
        case .artifacts: return "浏览对话中生成的页面、报告与对比图"
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

/// 通用多选工具条：取消（Esc）/ 分隔线 / 「已选 N <unit>」（空选显示 emptyText）/ 右侧自定义动作。
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

/// 右侧栏浏览面板：网格入口（含打开浏览器）+ 浏览页面切换
struct BrowsePanelView: View {
    @ObservedObject var manager: RightSidebarManager
    @ObservedObject var billVM: BillViewModel
    @ObservedObject var memoryVM: MemoryViewModel
    @ObservedObject var artifactVM: ArtifactViewModel
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
    #if os(macOS)
    /// 文档详情页的两个开关（原本在详情页自己的二级顶栏上，现已并入页头）。
    /// 与 `DocumentFullView` 共用同一批 key，改一边另一边立刻跟上。
    @AppStorage("xhan_doc_annot_open") private var docAnnotOpen = true
    @AppStorage("xhan_doc_find_open") private var docFindOpen = false
    #endif
    /// 多选删除二次确认。
    @State private var isConfirmingBatchDelete = false
    /// 图片多选删除二次确认。
    @State private var isConfirmingImageBatchDelete = false
    /// 灵阅多选删除二次确认。
    @State private var isConfirmingArtifactBatchDelete = false
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
                    // 灵阅多选跨端一致，判断放在平台分支之外（文档/图片的多选是 macOS-only）
                    if isArtifactSelectionActive {
                        artifactSelectionHeader
                    } else {
                        #if os(macOS)
                        if isDocSelectionActive {
                            docSelectionHeader
                        } else if isImageSelectionActive {
                            imageSelectionHeader
                        } else {
                            panelHeader(panel)
                        }
                        #else
                        panelHeader(panel)
                        #endif
                    }
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
            imageVM.exitSelection()
            artifactVM.exitSelection()
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
        .onChange(of: documentVM.audienceView) { _, _ in
            // 切读者层级即重载列表（load 会回第一页）；开关不持久化，下次进面板回默认。
            Task { await documentVM.load() }
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
        // 9 格（浏览器 + 8 个面板）：宽 5 列 × 2 行、窄 2 列 × 5 行。窄态高度不够时靠 ScrollView 兜住，
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

    #if os(macOS)
    /// 文档详情态：页头返回先回文档列表（详情页原来的二级顶栏已并入页头）
    private var isDocDetail: Bool {
        manager.browsePanelSelection == .documents && documentVM.openDocument != nil
    }
    #endif

    /// 页头返回按钮的 help：文档详情态回列表，其余回网格
    private var backHelp: String {
        #if os(macOS)
        if isDocDetail { return "返回文档列表" }
        #endif
        return "返回网格"
    }

    private func panelHeader(_ panel: BrowsePanel) -> some View {
        HStack(spacing: 8) {
            Button {
                #if os(macOS)
                if isDocDetail {
                    // 详情 → 列表；再按一次才回网格（面包屑式两级返回）
                    docFindOpen = false
                    documentVM.openDocument = nil
                    return
                }
                #endif
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
            .help(backHelp)

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

                if documentVM.mode != .trash {
                    // 读者层级：单按钮三态循环（成品 → 底稿 → 全部），列表与词云通用。状态不持久化。
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

            if panel == .images {
                Button {
                    imageVM.enterSelection()
                } label: {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("多选管理")
                .disabled(imageVM.items.isEmpty)
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

            // 多选入口：文档/图片/灵阅统一放在页头这颗图标上（灵阅那份跨端，故在平台分支之外）
            if panel == .artifacts {
                Button {
                    artifactVM.enterSelection()
                } label: {
                    Image(systemName: "checkmark.circle")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("多选管理")
                .accessibilityLabel("多选管理")
                .disabled(artifactVM.items.isEmpty)
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

            #if os(macOS)
            // 文档详情页：划线与批注 / 查找并进页头（详情页自己那条二级顶栏已去掉）
            if isDocDetail {
                Button {
                    withAnimation { docAnnotOpen.toggle() }
                } label: {
                    Image(systemName: "list.bullet.rectangle")
                        .font(.system(size: 12))
                        .foregroundColor(docAnnotOpen ? Color("appAccent") : .secondary)
                }
                .buttonStyle(.plain)
                .help("划线与批注面板")

                headerSearchToggle(active: docFindOpen, help: "在文档中查找（⌘F）") { docFindOpen.toggle() }
                    .keyboardShortcut("f", modifiers: .command)
            }
            #endif

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

    /// 图片批量删除确认文案：选中含「使用中」或非聊天来源时额外提示会裂图。
    /// 放在 `#if os(macOS)` 之外：确认框挂在 panelContent 的 images 分支（跨平台），iOS 编译也需要它。
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

    /// 灵阅多选态是否生效：**必须与当前面板绑定**——状态是靠 `onChange(of: browsePanelSelection)` 清的，
    /// 而那是在 body 之后执行；从灵阅多选态直接切到别的面板时，只看 `isSelecting` 会有一帧画出「灵阅工具条 + 别的面板」。
    /// 跨端可用，所以不在 `#if os(macOS)` 里。
    private var isArtifactSelectionActive: Bool {
        manager.browsePanelSelection == .artifacts && artifactVM.isSelecting
    }

    #if os(macOS)
    /// 文档列表多选态是否生效（选择工具条取代常规页头）。
    private var isDocSelectionActive: Bool {
        documentVM.isSelectingDocuments && documentVM.mode == .list && documentVM.openDocument == nil
    }

    /// 图片多选态是否生效（选择工具条取代常规页头）。
    private var isImageSelectionActive: Bool {
        imageVM.isSelecting && manager.browsePanelSelection == .images
    }

    /// 图片多选工具条：取消（左）/ 已选张数 / 全选 + 删除（右）。
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

    /// 多选工具条：取消（左）/ 已选篇数 / 删除 + 下载（右）。
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

    /// 灵阅多选工具条：取消（左）/ 已选页数 / 全选 + 删除（右）。与文档、图片同一款 `SelectionToolbar`。
    /// 跨端可用（灵阅多选在 iOS 上也有），所以不在 `#if os(macOS)` 里。
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
        case .artifacts: return artifactVM.isLoading
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
        case .artifacts: await artifactVM.load()
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
        case .artifacts:
            ArtifactPage(viewModel: artifactVM)
                // 挂灵阅面板子树，别和 body 上的文档删除对话框叠在同一节点
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
            DocumentPage(viewModel: documentVM)
        case .images:
            ImagePage(viewModel: imageVM, baseURL: baseURL, token: token)
                // 挂图片面板子树，别和 body 上的文档删除对话框叠在同一节点
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
