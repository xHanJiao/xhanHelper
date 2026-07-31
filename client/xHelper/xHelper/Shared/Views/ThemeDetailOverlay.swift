import SwiftUI
import MarkdownUI

struct ThemeDetailOverlay: View {
    let theme: AppTheme
    let onClose: () -> Void

    var statusColor: Color {
        switch theme.status {
        case "active": return .green
        case "closed": return .secondary
        default: return .mint
        }
    }

    var body: some View {
        ZStack {
            Color.black.opacity(0.2)
                .ignoresSafeArea()
                .onTapGesture { onClose() }

            DetailSheet(onClose: onClose) {
                VStack(alignment: .leading, spacing: 14) {
                    HStack(spacing: 8) {
                        Text(theme.name)
                            .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))
                        Text(theme.status)
                            .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                            .foregroundColor(statusColor)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 2)
                            .background(statusColor.opacity(0.12))
                            .clipShape(Capsule())
                    }

                    Markdown(theme.description)
                        .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
                        .markdownCodeSyntaxHighlighter(.plainText)

                    if !theme.stateText.isEmpty {
                        Divider()
                        Markdown(theme.stateText)
                            .font(.system(size: FontSizeManager.shared.scaledSize * 0.94))
                            .foregroundColor(.secondary)
                    }
                }
            }
            .frame(minWidth: 400, maxWidth: 700)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .padding(40)
            .transition(.opacity.combined(with: .scale(0.95)))
        }
        .animation(.easeOut(duration: 0.2), value: theme.id)
    }
}
