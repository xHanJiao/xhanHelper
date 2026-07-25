import SwiftUI
import BeautifulMermaid
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif
#if os(iOS)
import UIKit
#endif

// MARK: - Inline view (message bubble)

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
            FullScreenMermaidView(code: code, theme: theme)
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
                .frame(maxHeight: 250)
                .scaleEffect(y: -1)
        } else {
            ProgressView()
                .frame(maxWidth: .infinity)
                .frame(height: 250)
        }
    }
}

// MARK: - Full-screen view with zoom & save

#if os(macOS)
struct FullScreenMermaidView: View {
    let code: String
    let theme: DiagramTheme

    @State private var renderedImage: NSImage? = nil
    @State private var currentScale: CGFloat = 1.0
    @State private var lastScale: CGFloat = 1.0

    private let renderScale: CGFloat = 3.0

    var body: some View {
        ScrollView([.horizontal, .vertical], showsIndicators: true) {
            if let img = renderedImage {
                Image(nsImage: img)
                    .resizable()
                    .frame(
                        width: img.size.width / renderScale * currentScale,
                        height: img.size.height / renderScale * currentScale
                    )
                    .gesture(
                        MagnificationGesture()
                            .onChanged { value in
                                let delta = value / lastScale
                                lastScale = value
                                currentScale = min(max(currentScale * delta, 0.3), 5.0)
                            }
                            .onEnded { _ in lastScale = 1.0 }
                    )
                    .onTapGesture(count: 2) {
                        withAnimation(.easeInOut) { currentScale = 1.0 }
                    }
                    .contextMenu {
                        Button("保存图片...") { saveImage() }
                    }
                    .padding(40)
            } else {
                ProgressView()
                    .frame(width: 700, height: 400)
            }
        }
        .frame(minWidth: 700, idealWidth: 900, minHeight: 400)
        .task { renderImage() }
    }

    private func renderImage() {
        let renderer = MermaidImageRenderer()
        renderer.theme = theme
        renderer.scale = renderScale
        if let data = try? renderer.renderPNG(from: code) {
            renderedImage = NSImage(data: data)
        }
    }

    private func saveImage() {
        let renderer = MermaidImageRenderer()
        renderer.theme = theme
        renderer.scale = 2.0
        guard let data = try? renderer.renderPNG(from: code) else { return }

        let savePanel = NSSavePanel()
        savePanel.allowedContentTypes = [.png]
        savePanel.nameFieldStringValue = "diagram.png"
        savePanel.begin { response in
            if response == .OK, let url = savePanel.url {
                try? data.write(to: url)
            }
        }
    }
}

#endif

#if os(iOS)
struct FullScreenMermaidView: View {
    let code: String
    let theme: DiagramTheme

    @State private var renderedImage: UIImage? = nil
    @State private var currentScale: CGFloat = 1.0
    @State private var lastScale: CGFloat = 1.0
    @State private var showShareSheet = false

    private let renderScale: CGFloat = 3.0

    var body: some View {
        ScrollView([.horizontal, .vertical], showsIndicators: true) {
            if let img = renderedImage {
                Image(uiImage: img)
                    .resizable()
                    .frame(
                        width: img.size.width / renderScale * currentScale,
                        height: img.size.height / renderScale * currentScale
                    )
                    .gesture(
                        MagnificationGesture()
                            .onChanged { value in
                                let delta = value / lastScale
                                lastScale = value
                                currentScale = min(max(currentScale * delta, 0.3), 5.0)
                            }
                            .onEnded { _ in lastScale = 1.0 }
                    )
                    .onTapGesture(count: 2) {
                        withAnimation(.easeInOut) { currentScale = 1.0 }
                    }
                    .onLongPressGesture { showShareSheet = true }
                    .padding(40)
            } else {
                ProgressView()
            }
        }
        .task { renderImage() }
        .sheet(isPresented: $showShareSheet) {
            if let data = try? renderPNGData(), let uiImage = UIImage(data: data) {
                ShareSheet(activityItems: [uiImage])
            }
        }
    }

    private func renderImage() {
        let renderer = MermaidImageRenderer()
        renderer.theme = theme
        renderer.scale = renderScale
        if let data = try? renderer.renderPNG(from: code) {
            renderedImage = UIImage(data: data)
        }
    }

    private func renderPNGData() throws -> Data {
        let renderer = MermaidImageRenderer()
        renderer.theme = theme
        renderer.scale = 2.0
        return try renderer.renderPNG(from: code)
    }
}

struct ShareSheet: UIViewControllerRepresentable {
    let activityItems: [Any]

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: activityItems, applicationActivities: nil)
    }

    func updateUIViewController(_ uiViewController: UIActivityViewController, context: Context) {}
}
#endif
