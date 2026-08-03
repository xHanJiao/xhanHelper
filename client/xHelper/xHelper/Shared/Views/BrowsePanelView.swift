import SwiftUI
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

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .bills:     return "yensign.circle"
        case .memories:  return "brain.head.profile"
        case .notes:     return "note.text"
        case .documents: return "doc.text"
        case .themes:    return "tag"
        }
    }

    var help: String {
        switch self {
        case .bills:     return "查看账单记录与统计"
        case .memories:  return "长期记忆与语义搜索"
        case .notes:     return "笔记与标签检索"
        case .documents: return "Markdown 文档与版本"
        case .themes:    return "主题状态追踪"
        }
    }
}

/// 右侧栏浏览面板：2×3 网格入口（含打开浏览器）+ 浏览页面切换
struct BrowsePanelView: View {
    @ObservedObject var manager: RightSidebarManager
    @ObservedObject var billVM: BillViewModel
    @ObservedObject var memoryVM: MemoryViewModel
    @ObservedObject var noteVM: NoteViewModel
    @ObservedObject var documentVM: DocumentViewModel
    @ObservedObject var themeVM: ThemeViewModel
    let baseURL: String
    let token: String

    /// 根据网格可用宽度自适应列：先维持 110 的格子宽、把剩余空间给间距；
    /// 空间不足时间距压到最小、格子随之收缩；空间富余时间距封顶、格子保持 110。
    private func adaptiveColumns(width: CGFloat) -> (columns: [GridItem], spacing: CGFloat) {
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
    }

    var body: some View {
        VStack(spacing: 0) {
            if let panel = manager.browsePanelSelection {
                panelHeader(panel)
                Divider()
                panelContent(panel)
            } else {
                grid
            }
        }
    }

    // MARK: - 网格

    private var grid: some View {
        // 固定 6 格无需滚动，垂直居中展示；间距/格子随可用宽度自适应收缩
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
