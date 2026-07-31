import SwiftUI
import MarkdownUI

struct AgentDetailOverlay: View {
    let agent: Agent
    let onClose: () -> Void

    var body: some View {
        ZStack {
            Color.black.opacity(0.2)
                .ignoresSafeArea()
                .onTapGesture { onClose() }

            DetailSheet(onClose: onClose) {
                VStack(alignment: .leading, spacing: 14) {
                    Text(agent.name)
                        .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))

                    Markdown(agent.systemPrompt)
                        .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
                        .markdownCodeSyntaxHighlighter(.plainText)
                }
            }
            .frame(minWidth: 400, maxWidth: 700)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .padding(40)
            .transition(.opacity.combined(with: .scale(0.95)))
        }
        .animation(.easeOut(duration: 0.2), value: agent.id)
    }
}
