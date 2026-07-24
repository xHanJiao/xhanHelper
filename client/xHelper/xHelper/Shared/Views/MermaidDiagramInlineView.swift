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

            ScrollView([.horizontal, .vertical], showsIndicators: false) {
                MermaidDiagramView(
                    source: code,
                    theme: colorScheme == .dark ? .zincDark : .zincLight
                )
            }
            .frame(maxWidth: .infinity)
            .frame(height: min(400, idealHeight))
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 8)
    }

    private var idealHeight: CGFloat {
        let lines = code.split(separator: "\n").count
        return CGFloat(max(200, min(400, lines * 20)))
    }
}
