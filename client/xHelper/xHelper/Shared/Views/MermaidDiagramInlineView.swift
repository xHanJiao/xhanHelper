import SwiftUI
import BeautifulMermaid

struct MermaidDiagramInlineView: View {
    let code: String
    @Environment(\.colorScheme) var colorScheme

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("图表")
                .font(.caption)
                .foregroundColor(.secondary)

            MermaidDiagramView(
                source: code,
                theme: colorScheme == .dark ? .zincDark : .zincLight
            )
            .frame(maxWidth: .infinity)
            .frame(height: 250)
            .clipped()
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 8)
    }
}
