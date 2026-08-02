import SwiftUI

struct NavigationToolbar: View {
    @ObservedObject var router: NavigationRouter
    @ObservedObject var rightSidebar: RightSidebarManager
    @ObservedObject var chatTabManager: ChatTabManager
    let navLeadingOffset: CGFloat
    @Environment(\.openSettings) private var openSettings

    var body: some View {
        HStack(spacing: 0) {
            Spacer()
                .frame(width: navLeadingOffset)

            // < > 会话历史导航：仅聊天区显示（其他功能区隐藏）
            if router.currentTab == .chat {
                CapsuleGroup {
                    CapsuleButton(systemName: "chevron.left", enabled: chatTabManager.canGoBack) {
                        Task { await chatTabManager.goBackHistory() }
                    }
                    .keyboardShortcut("[", modifiers: .command)

                    Divider()
                        .frame(height: 14)

                    CapsuleButton(systemName: "chevron.right", enabled: chatTabManager.canGoForward) {
                        Task { await chatTabManager.goForwardHistory() }
                    }
                    .keyboardShortcut("]", modifiers: .command)
                }
            }

            Spacer()

            if !router.currentToolbarActions.isEmpty {
                CapsuleGroup {
                    ForEach(router.currentToolbarActions) { action in
                        CapsuleButton(systemName: action.icon, enabled: true) {
                            action.action()
                        }
                    }
                }

                separator
            }

            CapsuleGroup {
                CapsuleRefreshButton(
                    disabled: !router.canRefreshCurrentTab,
                    disabledMessage: router.refreshDisabledMessage
                ) {
                    router.refresh()
                }
                .keyboardShortcut("r", modifiers: .command)

                Divider()
                    .frame(height: 14)

                CapsuleButton(systemName: "gear", enabled: true) {
                    openSettings()
                }

                Divider()
                    .frame(height: 14)

                CapsuleButton(systemName: "sidebar.trailing", enabled: true) {
                    rightSidebar.toggle()
                }
            }
        }
        .padding(.horizontal, Layout.toolbarHorizontalPadding)
        .padding(.vertical, 6)
    }

    private var separator: some View {
        Divider()
            .frame(width: 1)
            .frame(height: 18)
            .padding(.horizontal, 4)
            .opacity(0.2)
    }
}

private struct CapsuleIcon: View {
    let systemName: String
    let showBackground: Bool

    var body: some View {
        Image(systemName: systemName)
            .font(.system(size: 16, weight: .medium))
            .foregroundColor(.primary.opacity(0.7))
            .frame(width: 26, height: 26)
            .modifier(CapsuleButtonBackground(show: showBackground))
    }
}

private struct CapsuleButtonBackground: ViewModifier {
    let show: Bool

    func body(content: Content) -> some View {
        if show {
            content.background(.quaternary.opacity(0.5), in: Circle())
        } else {
            content
        }
    }
}

private struct CapsuleButton: View {
    let systemName: String
    let enabled: Bool
    let action: () -> Void
    @State private var isHovered = false

    var body: some View {
        Button(action: action) {
            CapsuleIcon(systemName: systemName, showBackground: isHovered && enabled)
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .opacity(enabled ? 1 : 0.25)
        .onHover { isHovered = $0 }
    }
}

private struct CapsuleRefreshButton: View {
    let disabled: Bool
    let disabledMessage: String?
    let action: () -> Void
    @State private var isAnimating = false
    @State private var isHovered = false

    private var isDisabled: Bool { disabled || isAnimating }

    var body: some View {
        Button {
            guard !isDisabled else { return }
            action()
            withAnimation(.interactiveSpring(response: 0.3, dampingFraction: 0.5)) {
                isAnimating = true
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                withAnimation(.interactiveSpring(response: 0.3, dampingFraction: 0.5)) {
                    isAnimating = false
                }
            }
        } label: {
            CapsuleIcon(systemName: "arrow.clockwise", showBackground: isHovered && !isDisabled)
                .rotationEffect(.degrees(isAnimating ? 360 : 0))
                .scaleEffect(isAnimating ? 0.85 : 1.0)
        }
        .buttonStyle(.plain)
        .disabled(isDisabled)
        .opacity(isDisabled ? 0.25 : 1)
        .help(disabledMessage ?? "")
        .onHover { isHovered = $0 }
    }
}

private struct CapsuleGroup<Content: View>: View {
    @ViewBuilder let content: () -> Content
    @State private var isHovered = false

    var body: some View {
        HStack(spacing: 4) {
            content()
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 5)
        .background(.ultraThinMaterial, in: Capsule())
        .overlay(
            Capsule()
                .stroke(.white.opacity(0.15), lineWidth: 0.5)
        )
        .shadow(
            color: .black.opacity(isHovered ? 0.2 : 0.12),
            radius: isHovered ? 8 : 4,
            y: isHovered ? 4 : 2
        )
        .animation(.easeInOut(duration: 0.2), value: isHovered)
        .onHover { isHovered = $0 }
    }
}
