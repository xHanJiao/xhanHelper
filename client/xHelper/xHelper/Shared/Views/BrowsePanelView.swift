import SwiftUI
import WebKit
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

/// 右侧栏浏览面板类型
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
    /// 记忆 / 图片 / 智能体 / 监控看板已移出首页，改从设置「内容与工具」进入；
    /// 枚举 case 必须保留——文档深链、顶栏 ⌘1/⌘2/⌘3 都直接选中它们。
    /// 见 docs/design/right-sidebar-home/design.md §7。
    /// 书架换掉了备忘卡（E10）：备忘仍可经问候行整行点击与设置入口进入。
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
    @ObservedObject var eventVM: EventViewModel
    @ObservedObject var agentVM: AgentViewModel
    @ObservedObject var imageVM: ImageViewModel
    /// 首页六个入口的最近对象 + 备忘面板的数字（六路轻请求 + 60s 缓存，见 `SidebarHomeViewModel`）。
    /// 挂在 `RightSidebarView`（与其它 VM 同处）而非首页自己：开浏览器 tab 时首页会被摘除，
    /// 挂在自己身上会让缓存随之销毁，每次回来都重新打六个接口。
    @ObservedObject var homeVM: SidebarHomeViewModel
    #if os(macOS)
    /// 本地 EPUB 书架（`Books/` 目录；不经服务端）
    @ObservedObject private var bookVM = BookViewModel.shared
    #endif
    let baseURL: String
    let token: String

    /// 监控看板 WebView 共享状态（header 刷新按钮 + WebView 共用）
    @StateObject private var dashboardWebModel = XHWebViewModel()

    /// 浏览器卡的信息行：未打开浏览器时取历史最新三条
    @ObservedObject private var historyStore = HistoryStore.shared

    /// 缓存击穿戳：每次打开面板/点刷新自增，URL 带 ?t= 强制回源拉新 index.html。
    /// 服务器静态资源无 Cache-Control，WKWebView 启发式缓存会让 reloadFromOrigin 命中旧文档，
    /// 只有换 URL 才能绕过。
    @State private var dashboardBust = Int(Date().timeIntervalSince1970)

    /// 文档/账单页头搜索图标展开态 + 搜索框草稿（切页复位，同一时刻至多开一个）。
    @State private var docSearchOpen = false
    @State private var billSearchOpen = false
    @State private var docSearchDraft = ""
    @State private var billSearchDraft = ""
    /// 灵阅词云的标签高亮搜索（纯本地；列表模式没有搜索框）。
    @State private var artifactSearchOpen = false
    @State private var artifactSearchDraft = ""
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

    // MARK: - 首页（问候行 + 六个入口卡）
    //
    // 设计见 docs/design/right-sidebar-home/design-cards-v2.md：
    // 卡片顶对齐；档位随卡宽四档降级，内容块封顶 1320 居中。
    // 宽卡的信息行已经列出最近对象，所以不再有底部「最近」页脚（2026-11-12 用户拍板删掉）。

    private func homeColumns(_ count: Int) -> [GridItem] {
        Array(repeating: GridItem(.flexible(), spacing: 12), count: count)
    }

    private var home: some View {
        GeometryReader { geo in
            let contentWidth = max(0, min(geo.size.width - 52, SidebarHomeFormat.homeContentMaxWidth))
            // 卡高不跟可用高走（2026-11-13 拍板 A）：恒 164 / 3 行，窗口矮就整页滚
            let metrics = SidebarHomeFormat.homeMetrics(contentWidth: contentWidth)
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    greeting
                    LazyVGrid(columns: homeColumns(metrics.columns), spacing: 12) {
                        browserCard(metrics: metrics)
                        ForEach(BrowsePanel.homePanels) { panel in
                            entryCard(panel, metrics: metrics)
                        }
                    }
                    .padding(.top, 16)

                    // 高度吃紧时先让这口留白让位（0 起，撑开时照旧垫底）
                    Spacer(minLength: 0)
                }
                .padding(.vertical, 18)
                .frame(width: contentWidth)
                // 撑满高度让内容顶对齐；封顶后整块水平居中、两侧留白
                .frame(maxWidth: .infinity, minHeight: geo.size.height, alignment: .top)
            }
        }
        .task {
            await homeVM.refresh(baseURL: baseURL, token: token)
            #if os(macOS)
            // 书架卡要「N 本 / 最近在读」，本地扫描一次（毫秒级）
            await bookVM.load()
            #endif
        }
    }

    private var greeting: some View {
        // 每分钟重算：问候语整点换档（「下午好」→「晚上好」），日期跨零点换。
        // 用 TimelineView 而不是 Timer —— 视图被摘除时自动停，不用管取消。
        // 不能用 `.periodic(from: Date(), by:)`：body 每次重算都换起点，tick 会被一直往后推。
        TimelineView(.everyMinute) { context in
            HomeGreetingView(
                now: context.date,
                pendingReminders: homeVM.stats.pendingReminders,
                events: homeVM.stats.events,
                monthSpend: homeVM.stats.monthSpend
            ) {
                manager.browsePanelSelection = .reminders
            }
        }
    }

    /// 打开着的网页标签：排除收藏夹 / 历史 / 配置 / 本页资源与灵阅产物 tab。
    /// 「N 个标签」和信息行都只算这些，不然收藏夹开着也会被数成一个标签。
    private var webTabs: [RightSidebarTab] {
        manager.tabs.filter {
            !RightSidebarManager.specialTabIDs.contains($0.id) && !RightSidebarManager.isArtifactTab($0.id)
        }
    }

    private func browserCard(metrics: HomeCardMetrics) -> some View {
        let count = webTabs.count
        // 有标签就列标签（点行切过去）；没打开浏览器就列历史最新三条（点行在新标签里打开）。
        let rows: [HomeEntryCard.InfoRow]
        if webTabs.isEmpty {
            rows = historyRows
        } else {
            rows = webTabs.prefix(3).map { tab in
                HomeEntryCard.InfoRow(
                    id: "tab:" + tab.id,
                    title: tab.title,
                    // 不显示 host（用户 2026-11-13）：favicon 已经表达站点，右侧留给「多久没看」
                    // 这类真信息；标签行没有可比的时间字段，就不填空话。
                    detail: nil,
                    icon: tab.host.map { HomeEntryCard.InfoIcon.favicon(host: $0) }
                        ?? .monogram(SidebarHomeFormat.monogram(tab.title))
                ) {
                    manager.activeTabId = tab.id
                    BrowserToolProvider.shared.selectTab(tab.id)
                }
            }
        }
        return HomeEntryCard(
            asset: "sidebar_browser",
            title: "浏览器",
            primary: count == 0 ? "未打开" : "\(count) 个标签",
            // 「上次停在 X」已删除：有历史时信息行已经给出，全新环境历史为空时它也没内容可说。
            secondary: nil,
            tint: Color("accentSecondary"),
            layout: metrics.layout,
            width: metrics.cardWidth,
            height: metrics.cardHeight,
            infoRows: rows
        ) {
            BrowserToolProvider.shared.openBlankTab()
        }
    }

    /// 未打开浏览器时的信息行：历史最新三条，点行即新开标签。
    /// 右侧用相对时间而不是 host——favicon 已经在表达站点，「多久没看」才是新信息。
    /// shortcut: 想改成显示 host 就换 detail 这一行，其余不用动。
    private var historyRows: [HomeEntryCard.InfoRow] {
        #if os(macOS)
        historyStore.entries.prefix(3).map { entry in
            HomeEntryCard.InfoRow(
                id: "history:" + entry.id.uuidString,
                title: entry.title,
                detail: SidebarHomeFormat.relativeTime(entry.visitedAt),
                icon: entry.host.isEmpty
                    ? .monogram(SidebarHomeFormat.monogram(entry.title))
                    : .favicon(host: entry.host)
            ) {
                // history.json 是本地文件，旧数据/手改数据可能带非法 URL —— 不能点了没反应。
                guard let url = URL(string: entry.url),
                      let scheme = url.scheme?.lowercased(),
                      scheme == "http" || scheme == "https"
                else {
                    LogBuffer.shared.warning("sidebar home: 无效历史 URL：\(entry.url.prefix(120))")
                    ToastCenter.shared.show("链接无效，无法打开")
                    return
                }
                BrowserToolProvider.shared.openURL(url)
            }
        }
        #else
        []
        #endif
    }

    private func entryCard(_ panel: BrowsePanel, metrics: HomeCardMetrics) -> some View {
        let s = homeVM.stats
        let card: (asset: String, primary: String, secondary: String?) = {
            switch panel {
            case .bills:
                return ("sidebar_bills",
                        s.monthSpend.map { "本月 " + SidebarHomeFormat.money($0) } ?? "—",
                        s.monthBillCount.map { "\($0) 笔" })
            #if os(macOS)
            case .books:
                // 最近在读：只认有进度的书（排序会用 importedAt 兜底，未读的新书不能算「在读」）
                let reading = bookVM.books.first { $0.progress != nil }
                return ("sidebar_books",
                        bookVM.books.isEmpty ? "—" : "\(bookVM.books.count) 本",
                        reading.map { "最近在读《\($0.title)》" } ?? "暂无在读")
            #endif
            case .reminders:
                let overdue = homeVM.reminders.filter { SidebarHomeFormat.isOverdue($0.deadline) }.count
                let next = homeVM.reminders.first { !SidebarHomeFormat.isOverdue($0.deadline) }
                // 只有全过期时才提过期条数；有「下一条」就先说下一条。
                let secondary = next.flatMap { SidebarHomeFormat.dueText($0.deadline) }
                    .map { "下一条 \($0)" }
                    ?? (overdue > 0 ? "\(overdue) 条已过期" : nil)
                return ("sidebar_reminder",
                        s.pendingReminders.map { "\($0) 条备忘" } ?? "—",
                        secondary)
            case .artifacts:
                return ("sidebar_artifact",
                        s.artifacts.map { "\($0) 页" } ?? "—",
                        s.artifactsCreatedText.map { "最近 \($0)" })
            case .documents:
                return ("sidebar_document",
                        s.documents.map { "\($0) 篇" } ?? "—",
                        s.documentsUpdatedText.map { "最近编辑 \($0)" })
            case .events:
                return ("sidebar_events",
                        s.events.map { "\($0) 个事件" } ?? "—",
                        s.eventsUpdatedText.map { "最近更新 \($0)" })
            case .memories, .images, .agents, .dashboard:
                // 已从首页移出（走设置），这里只为穷尽 switch
                return ("sidebar_document", "", nil)
            }
        }()
        let rows = infoRows(for: panel)
        return HomeEntryCard(
            asset: card.asset,
            title: panel.rawValue,
            primary: card.primary,
            secondary: card.secondary,
            tint: tint(for: panel),
            layout: metrics.layout,
            width: metrics.cardWidth,
            height: metrics.cardHeight,
            infoRows: rows
        ) {
            manager.browsePanelSelection = panel
        }
    }

    /// 信息档的三条最近对象。全部复用 `SidebarHomeViewModel` 已有的取数，不额外请求。
    /// 图标只落在已有字段上：账单 `category1` 映射、备忘按是否过期、无类型字段的域用标题首字。
    private func infoRows(for panel: BrowsePanel) -> [HomeEntryCard.InfoRow] {
        let openPanel: () -> Void = { manager.browsePanelSelection = panel }
        switch panel {
        #if os(macOS)
        case .books:
            // 最近三本：书名 + 章号（未读则「未读」）。点行直接进那本书，
            // 只切到书架面板会让人多点一次（卡片文字区仍走 openPanel）
            return bookVM.books.prefix(3).map { book in
                let detail: String
                if let progress = book.progress,
                   book.effectiveSections.count > book.chapterCount,
                   let section = book.section(at: progress.sectionIndex ?? 0),
                   !section.title.trimmingCharacters(in: .whitespaces).isEmpty {
                    // 切过节的书：节标题比「第 2/2 章」有用得多
                    detail = section.title
                } else if let progress = book.progress, book.hasChapterNavigation {
                    detail = "第 \(progress.chapterIndex + 1)/\(book.chapterCount) 章"
                } else {
                    detail = book.progress == nil ? "未读" : "已读 \(Int((book.progressFraction * 100).rounded()))%"
                }
                return HomeEntryCard.InfoRow(
                    id: "book:" + book.id,
                    title: book.title,
                    detail: detail,
                    icon: .monogram(SidebarHomeFormat.monogram(book.title)),
                    action: {
                        manager.browsePanelSelection = .books
                        bookVM.openBook = book
                    }
                )
            }
        #endif
        case .bills:
            return homeVM.recentBills.prefix(3).map { bill in
                HomeEntryCard.InfoRow(
                    id: "bill:" + bill.id,
                    title: bill.item,
                    detail: SidebarHomeFormat.money(bill.amount),
                    icon: SidebarHomeFormat.billIcon(category1: bill.category1)
                        .map { HomeEntryCard.InfoIcon.symbol($0) }
                        ?? .monogram(SidebarHomeFormat.monogram(bill.item)),
                    action: openPanel
                )
            }
        case .reminders:
            return homeVM.reminders.prefix(3).map { reminder -> HomeEntryCard.InfoRow in
                let overdue = SidebarHomeFormat.isOverdue(reminder.deadline)
                return HomeEntryCard.InfoRow(
                    id: "reminder:" + reminder.id,
                    title: reminder.content,
                    detail: SidebarHomeFormat.dueRelative(reminder.deadline),
                    icon: overdue ? .warning("exclamationmark.circle") : .symbol("checkmark.circle"),
                    action: openPanel
                )
            }
        case .artifacts:
            return homeVM.recentArtifacts.prefix(3).map { artifact in
                HomeEntryCard.InfoRow(
                    id: "artifact:" + artifact.id,
                    title: artifact.title,
                    detail: LocalArtifactTime.parse(artifact.createdAt).map { SidebarHomeFormat.relativeTime($0) },
                    icon: .monogram(SidebarHomeFormat.monogram(artifact.title)),
                    action: { ArtifactOpenCoordinator.shared.open(id: artifact.id, title: artifact.title) }
                )
            }
        case .documents:
            return homeVM.recentDocuments.prefix(3).map { doc in
                HomeEntryCard.InfoRow(
                    id: "document:" + doc.id,
                    title: doc.title,
                    detail: LocalArtifactTime.parse(doc.updatedAt).map { SidebarHomeFormat.relativeTime($0) },
                    icon: .monogram(SidebarHomeFormat.monogram(doc.title)),
                    action: { DocumentSelectionCoordinator.shared.openDocument(id: doc.id) }
                )
            }
        case .events:
            return homeVM.recentEvents.prefix(3).map { event in
                HomeEntryCard.InfoRow(
                    id: "event:" + event.id,
                    title: event.name,
                    detail: LocalArtifactTime.parse(event.updatedAt).map { SidebarHomeFormat.relativeTime($0) },
                    icon: .monogram(SidebarHomeFormat.monogram(event.name)),
                    action: openPanel
                )
            }
        case .memories, .images, .agents, .dashboard:
            // 已从首页移出（走设置），这里只为穷尽 switch
            return []
        }
    }

    private func tint(for panel: BrowsePanel) -> Color {
        switch panel {
        case .bills:     return Color("assistantText")
        #if os(macOS)
        case .books:     return Color("appAccent")
        #endif
        case .reminders: return Color("appAccent")
        case .memories:  return Color("appAccent")
        case .artifacts: return Color("appAccent")
        case .documents: return Color("appAccent")
        case .events:    return Color("appAccent")
        case .images, .agents, .dashboard: return Color("appAccent")
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            if let panel = manager.browsePanelSelection {
                if panel == .dashboard {
                    // 监控看板自带 XHPanel header（含刷新按钮）
                    panelContent(panel)
                } else if isBookReader {
                    // 阅读页自带常驻 chrome（返回书架 / 章号 / 目录 / Aa）
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
                home
            }
        }
        .onChange(of: manager.browsePanelSelection) { _, newValue in
            if newValue == nil {
                docSearchOpen = false
                billSearchOpen = false
                artifactSearchOpen = false
            }
            // 阅读页只活在书架面板里：切走就关掉（进度已由 onDisappear 存过，回来从进度恢复）
            #if os(macOS)
            if newValue != .books {
                bookVM.openBook = nil
                // 阅读页退出时进度已写盘；书架列表还是打开前那份，重扫一次让卡片跟着更新
                Task { await bookVM.load() }
            }
            #endif
            // 离开文档面板即退出多选（选择工具条只在文档列表前台时有效）
            documentVM.exitDocumentSelection()
            imageVM.exitSelection()
            artifactVM.exitSelection()
        }
        .onChange(of: artifactVM.mode) { _, _ in
            // 切模式即收起灵阅的高亮搜索；离开词云顺手清掉本地高亮与多选
            artifactSearchOpen = false
            artifactSearchDraft = ""
            artifactVM.cloudQuery = ""
            artifactVM.clearSelection()
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

    /// 灵阅搜索：词云态是本地高亮，列表态是后端关键词（标题/摘要）。
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
            // 列表态：后端关键词（标题/摘要），与标签筛选取交集
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

    // MARK: - 浏览页面

    #if os(macOS)
    /// 文档详情态：页头返回先回文档列表（详情页原来的二级顶栏已并入页头）
    private var isDocDetail: Bool {
        manager.browsePanelSelection == .documents && documentVM.openDocument != nil
    }
    #endif

    /// 书阅读页在承前面板时自带整条 chrome，所以不叠 panelHeader。
    private var isBookReader: Bool {
        #if os(macOS)
        return manager.browsePanelSelection == .books && bookVM.openBook != nil
        #else
        return false
        #endif
    }

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
            if panel == .artifacts && artifactVM.mode == .list {
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

            #if os(macOS)
            // 书架：导入入口（拖拽走 BookshelfView 的 onDrop，同一条管线）
            if panel == .books {
                if bookVM.isImporting {
                    ProgressView()
                        .controlSize(.small)
                        .help("正在导入…")
                } else {
                    Button {
                        bookVM.presentImportPanel()
                    } label: {
                        Image(systemName: "tray.and.arrow.down")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("导入 EPUB")
                }
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

    /// 灵阅词云的最少页数上限（不低过当前值，避免 Stepper 范围越界）
    private var maxArtifactTagCount: Int {
        max(artifactVM.minTagCount, artifactVM.tags.first?.count ?? 1)
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
        #if os(macOS)
        case .books:     return bookVM.isLoading
        #endif
        case .reminders: return homeVM.isLoading
        case .memories:  return memoryVM.isLoading
        case .artifacts: return artifactVM.isLoading
        case .documents:
            if documentVM.openDocument != nil { return documentVM.isDetailLoading }
            return documentVM.mode == .trash ? documentVM.isTrashLoading : documentVM.isLoading
        case .images:    return imageVM.isLoading
        case .events:    return eventVM.isLoading
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
        #if os(macOS)
        case .books:     await bookVM.load()
        #endif
        case .reminders: await homeVM.refresh(baseURL: baseURL, token: token, force: true)
        case .memories:  await memoryVM.load()
        case .artifacts: await artifactVM.load()
        case .documents: await documentVM.loadCurrentMode()
        case .images:    await imageVM.load()
        case .events:    await eventVM.load()
        case .agents:    await agentVM.load()
        case .dashboard: break
        }
    }

    @ViewBuilder
    private func panelContent(_ panel: BrowsePanel) -> some View {
        switch panel {
        case .bills:
            BillPage(viewModel: billVM, baseURL: baseURL, token: token)
        #if os(macOS)
        case .books:
            if let book = bookVM.openBook {
                // 阅读页自带常驻 chrome（返回书架 / 章号 / 目录 / Aa），所以整页不套 panelHeader
                BookReaderView(book: book) {
                    bookVM.openBook = nil
                    // 回来后书架要显示新进度（meta.json 已被阅读页改写）
                    Task { await bookVM.load() }
                }
                .id(book.id)
            } else {
                BookshelfView(viewModel: bookVM)
            }
        #endif
        case .reminders:
            // 只读列表，数据与首页卡同源；直接从设置/顶栏进来时先补一次（60s 内命中缓存不重复请求）。
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
        case .events:
            EventPage(viewModel: eventVM)
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
