import SwiftUI

/// 右侧栏：浏览面板（默认态，无 tab 条）+ 浏览器标签页（tab 条 + 返回按钮）。
struct RightSidebarView: View {
    @ObservedObject var manager: RightSidebarManager
    @ObservedObject var billVM: BillViewModel
    @ObservedObject var memoryVM: MemoryViewModel
    @ObservedObject var noteVM: NoteViewModel
    @ObservedObject var documentVM: DocumentViewModel
    @ObservedObject var themeVM: ThemeViewModel
    let baseURL: String
    let token: String

    @State private var isHovered = false

    var body: some View {
        VStack(spacing: 0) {
            if !manager.isBrowseActive {
                browserTabBar
                Divider()
            }
            contentArea
        }
        .frame(width: manager.width)
        .modifier(CardBackgroundModifier(isLifted: isHovered))
        .onHover { isHovered = $0 }
    }

    // MARK: - Browser tab bar（仅浏览器态显示）

    private var browserTabBar: some View {
        HStack(spacing: 0) {
            // 返回浏览面板入口
            Button {
                manager.showBrowsePanel()
            } label: {
                HStack(spacing: 3) {
                    Image(systemName: "chevron.left").font(.system(size: 10, weight: .semibold))
                    Text("返回").font(.system(size: 11))
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 6)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("返回浏览面板")

            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 0) {
                    ForEach(manager.tabs) { tab in
                        browserTabButton(tab)
                    }
                }
                .frame(height: 32)
            }

            // 新空白页按钮（固定在 tab 条最右侧，不随 tab 滚动）
            Button {
                BrowserToolProvider.shared.openBlankTab()
            } label: {
                Image(systemName: "plus")
                    .font(.system(size: 11, weight: .semibold))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("新空白页")
            .padding(.horizontal, 4)
        }
        .padding(.leading, 6)
    }

    private func browserTabButton(_ tab: RightSidebarTab) -> some View {
        let isActive = manager.activeTabId == tab.id
        return HStack(spacing: 4) {
            Image(systemName: tab.icon).font(.system(size: 11))
            Text(tab.title).font(.system(size: 11)).lineLimit(1)
            Button { manager.closeTab(tab.id) } label: {
                Image(systemName: "xmark").font(.system(size: 8, weight: .bold))
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(
            isActive
                ? AnyShapeStyle(.regularMaterial)
                : AnyShapeStyle(.clear),
            in: UnevenRoundedRectangle(
                topLeadingRadius: 6, bottomLeadingRadius: 0,
                bottomTrailingRadius: 0, topTrailingRadius: 6
            )
        )
        .contentShape(Rectangle())
        .onTapGesture {
            manager.activeTabId = tab.id
            BrowserToolProvider.shared.selectTab(tab.id)
        }
    }

    // MARK: - Content

    @ViewBuilder
    private var contentArea: some View {
        if manager.isBrowseActive {
            BrowsePanelView(
                manager: manager,
                billVM: billVM,
                memoryVM: memoryVM,
                noteVM: noteVM,
                documentVM: documentVM,
                themeVM: themeVM,
                baseURL: baseURL,
                token: token
            )
        } else {
            ZStack {
                ForEach(manager.tabs) { tab in
                    tab.content
                        .opacity(tab.id == manager.activeTabId ? 1 : 0)
                        .allowsHitTesting(tab.id == manager.activeTabId)
                }
            }
        }
    }
}
