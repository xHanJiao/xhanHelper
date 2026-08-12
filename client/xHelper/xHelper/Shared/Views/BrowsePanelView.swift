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

    /// 监控看板前端地址：DEBUG 用 Vite dev server，RELEASE 用同源后端（nginx 已 serve）
    private var dashboardURL: URL? {
        #if DEBUG
        URL(string: "http://localhost:5173/admin")
        #else
        URL(string: baseURL + "/admin")
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
                    panelContent(panel)
                }
            } else {
                grid
            }
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
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
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
                            action: { dashboardWebModel.reload() }
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
