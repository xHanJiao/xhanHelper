import SwiftUI
import BeautifulMermaid

struct MermaidDiagramInlineView: View {
    let code: String
    @Environment(\.colorScheme) var colorScheme
    @State private var showFullScreen = false

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
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 8)
        .contentShape(Rectangle())
        .onTapGesture {
            showFullScreen = true
        }
        .sheet(isPresented: $showFullScreen) {
            ScrollView([.horizontal, .vertical], showsIndicators: true) {
                MermaidDiagramView(
                    source: code,
                    theme: colorScheme == .dark ? .zincDark : .zincLight
                )
                .padding(40)
            }
            .frame(minWidth: 700, idealWidth: 900, minHeight: 400)
        }
    }
}
