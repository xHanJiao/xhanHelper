import SwiftUI
import BeautifulMermaid

struct MermaidDiagramInlineView: View {
    let code: String
    @Environment(\.colorScheme) var colorScheme
    @State private var renderedImage: NSImage? = nil
    @State private var renderError: String? = nil
    @State private var showFullScreen = false

    private var theme: DiagramTheme {
        colorScheme == .dark ? .zincDark : .zincLight
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("图表")
                .font(.caption)
                .foregroundColor(.secondary)

            inlineContent
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 8)
        .contentShape(Rectangle())
        .onTapGesture { showFullScreen = true }
        .sheet(isPresented: $showFullScreen) {
            ScrollView([.horizontal, .vertical], showsIndicators: true) {
                MermaidDiagramView(source: code, theme: theme)
                    .padding(40)
            }
            .frame(minWidth: 700, idealWidth: 900, minHeight: 400)
        }
        .task {
            do {
                renderedImage = try await MermaidRenderer.renderImageAsync(source: code, theme: theme)
            } catch {
                renderError = error.localizedDescription
            }
        }
    }

    @ViewBuilder
    private var inlineContent: some View {
        if let error = renderError {
            VStack(spacing: 4) {
                Image(systemName: "exclamationmark.triangle")
                    .foregroundColor(.secondary)
                Text("渲染失败")
                    .font(.caption)
                    .foregroundColor(.secondary)
                Text(error)
                    .font(.caption2)
                    .foregroundColor(.secondary.opacity(0.7))
                    .lineLimit(3)
            }
            .frame(maxWidth: .infinity)
            .frame(height: 250)
        } else if let img = renderedImage {
            Image(nsImage: img)
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(maxWidth: .infinity, maxHeight: 250)
        } else {
            ProgressView()
                .frame(maxWidth: .infinity)
                .frame(height: 250)
        }
    }
}
