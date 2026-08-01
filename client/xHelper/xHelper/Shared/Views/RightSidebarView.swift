import SwiftUI

struct RightSidebarView: View {
    @ObservedObject var manager: RightSidebarManager
    @State private var isHovered = false

    var body: some View {
        VStack(spacing: 0) {
            tabBar
            Divider()
            contentArea
        }
        .frame(width: manager.width)
        .modifier(CardBackgroundModifier(isLifted: isHovered))
        .onHover { isHovered = $0 }
    }

    private var tabBar: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 0) {
                ForEach(manager.tabs) { tab in
                    tabButton(tab)
                }
            }
            .frame(height: 32)
            .padding(.horizontal, 6)
        }
    }

    private func tabButton(_ tab: RightSidebarTab) -> some View {
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

    @ViewBuilder
    private var contentArea: some View {
        if manager.tabs.isEmpty {
            RightSidebarEmptyView {
                BrowserToolProvider.shared.openBlankTab()
            }
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
