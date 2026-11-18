import SwiftUI
#if os(macOS)
import AppKit
#endif

/// 右栏首页（常驻的「首页」tab 内容）：问候行 + 入口卡。
///
/// 设计见 `docs/design/right-sidebar-home/`，tab 化见 `docs/design/right-sidebar-unified-tabs/`。
/// 所有入口都不再"切面板"，而是打开/激活对应 tab。
struct SidebarHomeView: View {
    @ObservedObject var manager: RightSidebarManager
    @ObservedObject var homeVM: SidebarHomeViewModel
    /// tab 内容只建一次，token/baseURL 实时取（本地后端重启会换 token）
    @ObservedObject var deviceManager: DeviceManager

    private var baseURL: String { deviceManager.baseURL }

    private var token: String { deviceManager.token ?? "" }

    #if os(macOS)
    @ObservedObject private var bookVM = BookViewModel.shared
    #endif
    @ObservedObject private var historyStore = HistoryStore.shared

    // MARK: - 首页（问候行 + 入口卡）

    private func homeColumns(_ count: Int) -> [GridItem] {
        Array(repeating: GridItem(.flexible(), spacing: 12), count: count)
    }

    var body: some View {
        GeometryReader { geo in
            let contentWidth = max(0, min(geo.size.width - 52, SidebarHomeFormat.homeContentMaxWidth))
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

                    Spacer(minLength: 0)
                }
                .padding(.vertical, 18)
                .frame(width: contentWidth)
                .frame(maxWidth: .infinity, minHeight: geo.size.height, alignment: .top)
            }
        }
        .task {
            await homeVM.refresh(baseURL: baseURL, token: token)
            #if os(macOS)
            await bookVM.load()
            #endif
        }
    }

    private var greeting: some View {
        TimelineView(.everyMinute) { context in
            HomeGreetingView(
                now: context.date,
                pendingReminders: homeVM.stats.pendingReminders,
                events: homeVM.stats.events,
                monthSpend: homeVM.stats.monthSpend
            ) {
                manager.openPanel(.reminders)
            }
        }
    }

    /// 打开着的网页标签（排除工具页与灵阅产物）
    private var webTabs: [RightSidebarTab] {
        manager.tabs.filter { $0.kind == .web }
    }

    private func browserCard(metrics: HomeCardMetrics) -> some View {
        let count = webTabs.count
        let rows: [HomeEntryCard.InfoRow]
        if webTabs.isEmpty {
            rows = historyRows
        } else {
            rows = webTabs.prefix(3).map { tab in
                HomeEntryCard.InfoRow(
                    id: "tab:" + tab.id,
                    title: tab.title,
                    detail: nil,
                    icon: tab.host.map { HomeEntryCard.InfoIcon.favicon(host: $0) }
                        ?? .monogram(SidebarHomeFormat.monogram(tab.title))
                ) {
                    manager.activateTab(id: tab.id)
                }
            }
        }
        return HomeEntryCard(
            asset: "sidebar_browser",
            title: "浏览器",
            primary: count == 0 ? "未打开" : "\(count) 个标签",
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
                let reading = bookVM.books.first { $0.progress != nil }
                return ("sidebar_books",
                        bookVM.books.isEmpty ? "—" : "\(bookVM.books.count) 本",
                        reading.map { "最近在读《\($0.title)》" } ?? "暂无在读")
            #endif
            case .reminders:
                let overdue = homeVM.reminders.filter { SidebarHomeFormat.isOverdue($0.deadline) }.count
                let next = homeVM.reminders.first { !SidebarHomeFormat.isOverdue($0.deadline) }
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
            manager.openPanel(panel)
        }
    }

    /// 信息档的三条最近对象（复用 SidebarHomeViewModel 的取数，不额外请求）
    private func infoRows(for panel: BrowsePanel) -> [HomeEntryCard.InfoRow] {
        let openPanel: () -> Void = { manager.openPanel(panel) }
        switch panel {
        #if os(macOS)
        case .books:
            return bookVM.books.prefix(3).map { book in
                let detail: String
                if let progress = book.progress,
                   book.effectiveSections.count > book.chapterCount,
                   let section = book.section(at: progress.sectionIndex ?? 0),
                   !section.title.trimmingCharacters(in: .whitespaces).isEmpty {
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
                    action: { manager.openBookTab(book) }
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
            return []
        }
    }

    private func tint(for panel: BrowsePanel) -> Color {
        switch panel {
        case .bills:     return Color("assistantText")
        #if os(macOS)
        case .books:     return Color("appAccent")
        #endif
        case .reminders, .memories, .artifacts, .documents, .events, .images, .agents, .dashboard:
            return Color("appAccent")
        }
    }
}
