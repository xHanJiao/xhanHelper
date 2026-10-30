import SwiftUI

#if os(macOS)
/// 侧栏导航行：标题 + 次信息行，当前项 / hover 给圆角底色。
/// 文档列表与灵阅列表共用同一样式（口径见 docs/design/document-sidebar-modes SM-7）。
struct SidebarNavRow: View {
    let title: String
    let subtitle: String
    let isCurrent: Bool
    let isHovered: Bool
    let action: () -> Void
    let onHover: (Bool) -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    Text(title)
                        .font(.system(size: 12, weight: isCurrent ? .semibold : .regular))
                        .foregroundColor(.primary)
                        .lineLimit(1)
                    if isCurrent {
                        Image(systemName: "checkmark")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundColor(Color("appAccent"))
                    }
                    Spacer(minLength: 0)
                }
                Text(subtitle)
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(
            isCurrent
                ? Color("appAccent").opacity(0.10)
                : (isHovered ? Color.primary.opacity(0.06) : .clear),
            in: RoundedRectangle(cornerRadius: 6)
        )
        .onHover(perform: onHover)
    }
}

/// 侧栏收起后的窄条：垂直居中一个 chevron.right，点它展开。
struct SidebarCollapsedStrip: View {
    let help: String
    let action: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            Spacer()
            Button(action: action) {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(help)
            .accessibilityLabel(help)
            Spacer()
        }
        .frame(width: Layout.docPanelStripWidth)
    }
}
#endif
