import SwiftUI

/// 可折叠的模式/信任切换组。
///
/// 布局里只有右侧的 chevron 按钮（34pt，固定在发送按钮左侧，编辑器宽度恒定）；
/// 两个胶囊（安全/信任、计划/聊天）作为 ZStack 浮层 offset 到 chevron 左侧，
/// 覆盖在编辑器上方弹出，不占布局空间。展开后 12s 自动收起。
struct CollapsibleModeControls: View {
    let context: InputBarContext
    let isPlanMode: Bool
    let trustedMode: Bool
    let onToggleMode: () -> Void
    let onToggleTrusted: () -> Void

    @State private var isExpanded = false
    @State private var collapseTask: Task<Void, Never>?

    var body: some View {
        IconButton(
            visibility: .always,
            context: context,
            symbol: isExpanded ? "chevron.right" : "chevron.left",
            tint: .secondary,
            tooltip: isExpanded ? "收起模式选项" : "展开模式选项",
            action: { toggle() }
        )
        .overlay(alignment: .topLeading) {
            // 浮层：胶囊，offset 到 chevron 左侧（覆盖编辑器上方；overlay 不影响布局尺寸）
            if isExpanded {
                HStack(spacing: 4) {
                    ModePill(context: context, isPlanMode: isPlanMode) {
                        onToggleMode()
                        resetTimer()
                    }
                    TrustedPill(context: context, trusted: trustedMode) {
                        onToggleTrusted()
                        resetTimer()
                    }
                }
                .offset(x: -76) // 34×2 胶囊 + 间距4 + 与 chevron 空隙4
                .transition(.scale(scale: 0.8).combined(with: .opacity))
            }
        }
        .animation(.spring(response: 0.35, dampingFraction: 0.8), value: isExpanded)
    }

    private func toggle() {
        isExpanded.toggle()
        if isExpanded {
            startAutoCollapse()
        } else {
            cancelAutoCollapse()
        }
    }

    /// 点击胶囊后重置自动收起计时（正在用就不收）。
    private func resetTimer() {
        if isExpanded {
            startAutoCollapse()
        }
    }

    private func startAutoCollapse() {
        collapseTask?.cancel()
        collapseTask = Task {
            try? await Task.sleep(for: .seconds(12))
            guard !Task.isCancelled else { return }
            withAnimation {
                isExpanded = false
            }
        }
    }

    private func cancelAutoCollapse() {
        collapseTask?.cancel()
    }
}
