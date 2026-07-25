import SwiftUI
import BeautifulMermaid
#if os(macOS)
import AppKit
import UniformTypeIdentifiers

private func mermaidTheme(for colorScheme: ColorScheme) -> DiagramTheme {
    if colorScheme == .dark {
        DiagramTheme(
            background: BMColor(hex: "#2C2E2D"),
            foreground: BMColor(hex: "#E8DECB"),
            line: BMColor(hex: "#4A4A4C"),
            accent: BMColor(hex: "#92C8B4"),
            muted: BMColor(hex: "#3A3A3C"),
            surface: BMColor(hex: "#242426"),
            border: BMColor(hex: "#5A5A5C")
        )
    } else {
        DiagramTheme(
            background: BMColor(hex: "#F2F7F3"),
            foreground: BMColor(hex: "#3B2E1F"),
            line: BMColor(hex: "#C4B99A"),
            accent: BMColor(hex: "#7DB5A0"),
            muted: BMColor(hex: "#E0D8C0"),
            surface: BMColor(hex: "#FFFFFF"),
            border: BMColor(hex: "#D0C8B0")
        )
    }
}
#endif
#if os(iOS)
import UIKit
#endif

// MARK: - Inline view (message bubble)

struct MermaidDiagramInlineView: View {
    let code: String
    @Environment(\.colorScheme) var colorScheme
    @State private var renderedImage: BMImage? = nil
    @State private var renderError: String? = nil
    @State private var showFullScreen = false

    private var theme: DiagramTheme {
#if os(macOS)
        mermaidTheme(for: colorScheme)
#else
        colorScheme == .dark ? .zincDark : .zincLight
#endif
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
        .task(id: colorScheme) {
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
#if os(macOS)
            Image(nsImage: img)
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(maxHeight: 250)
                .scaleEffect(y: -1)
#else
            Image(uiImage: img)
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(maxHeight: 250)
#endif
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
                    .scaleEffect(y: -1)
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
        .task(id: theme) { renderImage() }
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
        guard let img = renderedImage else { return }

        let flipped = NSImage(size: img.size)
        flipped.lockFocus()
        let transform = NSAffineTransform()
        transform.translateX(by: 0, yBy: img.size.height)
        transform.scaleX(by: 1, yBy: -1)
        transform.concat()
        img.draw(at: .zero, from: .zero, operation: .copy, fraction: 1)
        flipped.unlockFocus()

        guard let tiff = flipped.tiffRepresentation,
              let rep = NSBitmapImageRep(data: tiff),
              let pngData = rep.representation(using: .png, properties: [:]) else { return }

        let savePanel = NSSavePanel()
        savePanel.allowedContentTypes = [.png]
        savePanel.nameFieldStringValue = "diagram.png"
        savePanel.begin { response in
            if response == .OK, let url = savePanel.url {
                try? pngData.write(to: url)
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
    @Environment(\.dismiss) private var dismiss

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
        .task(id: theme) { renderImage() }
        .overlay(alignment: .topTrailing) {
            Button {
                dismiss()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.title2)
                    .foregroundColor(.secondary)
                    .background(.ultraThinMaterial, in: Circle())
            }
            .padding(12)
        }
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

    private func renderPNGData() throws -> Data? {
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
