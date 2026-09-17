import SwiftUI
import BeautifulMermaid

/// mermaid 主题（浅/深）。`DocumentReaderView` 把图预渲染成 PNG 时也要用同一套色，
/// 所以是 internal 而不是 fileprivate。
func mermaidTheme(for colorScheme: ColorScheme) -> DiagramTheme {
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

#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif
#if os(iOS)
import UIKit
#endif

// MARK: - Inline view (message bubble / document)

struct MermaidDiagramInlineView: View {
    let code: String
    @Environment(\.colorScheme) var colorScheme
    @State private var renderedImage: BMImage? = nil
    @State private var renderError: String? = nil

    private var theme: DiagramTheme {
        mermaidTheme(for: colorScheme)
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
        .onTapGesture {
            LightboxCenter.shared.present {
                MermaidLightboxView(code: code, theme: theme)
            }
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
            Image(platformImage: img)
                .resizable()
                .interpolation(.high)
                .aspectRatio(contentMode: .fit)
                #if os(macOS)
                .scaleEffect(y: -1)
                #endif
                .frame(maxHeight: 420)
        } else {
            ProgressView()
                .frame(maxWidth: .infinity)
                .frame(height: 250)
        }
    }
}

// MARK: - 浮层大图：默认适配窗口，可捏合放大，点 X / Esc 关闭

struct MermaidLightboxView: View {
    let code: String
    let theme: DiagramTheme

    @State private var renderedImage: BMImage? = nil
    /// 相对「适配窗口」的倍数：1.0 = 铺满可用空间
    @State private var zoom: CGFloat = 1.0
    @State private var lastZoom: CGFloat = 1.0
    #if os(iOS)
    @State private var showShareSheet = false
    #endif

    private let renderScale: CGFloat = 3.0
    private let margin: CGFloat = 24

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Spacer()
                Button {
                    LightboxCenter.shared.dismiss()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 20))
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .keyboardShortcut(.cancelAction)
                .help("关闭")
            }
            .padding(10)

            GeometryReader { geo in
                ScrollView([.horizontal, .vertical], showsIndicators: true) {
                    if let img = renderedImage {
                        // 渲染图的 size 已是自然点尺寸、像素是 3 倍，所以上限取 renderScale：
                        // 小图放到最大也只是 1:1 像素（清晰），且 fit ≤ 宽高比必然铺得下
                        let fit = min(
                            renderScale,
                            (geo.size.width - margin * 2) / img.size.width,
                            (geo.size.height - margin * 2) / img.size.height
                        )
                        Image(platformImage: img)
                            .resizable()
                            .interpolation(.high)
                            #if os(macOS)
                            .scaleEffect(y: -1)
                            #endif
                            .frame(width: img.size.width * fit * zoom,
                                   height: img.size.height * fit * zoom)
                            .padding(margin)
                            .contentShape(Rectangle())
                            .contextMenu {
                                #if os(macOS)
                                Button("保存图片...") { saveImage() }
                                #endif
                            }
                            .gesture(
                                MagnificationGesture()
                                    .onChanged { value in
                                        let delta = value / lastZoom
                                        lastZoom = value
                                        zoom = min(max(zoom * delta, 0.3), 5.0)
                                    }
                                    .onEnded { _ in lastZoom = 1.0 }
                            )
                            #if os(iOS)
                            .onLongPressGesture { showShareSheet = true }
                            #endif
                            .frame(minWidth: geo.size.width, minHeight: geo.size.height)
                    } else {
                        ProgressView()
                            .frame(width: geo.size.width, height: geo.size.height)
                    }
                }
            }
        }
        .contentShape(Rectangle())
        // 点浮层任意位置退出（含图片外的留白），和聊天图片预览一致；图片本身没有单击手势，
        // 单击会冒泡到这里，捏合缩放/右键保存不受影响
        .onTapGesture { LightboxCenter.shared.dismiss() }
        .frame(minWidth: 480, minHeight: 360)
        .task(id: theme) { renderImage() }
        #if os(iOS)
        .sheet(isPresented: $showShareSheet) {
            if let data = try? renderPNGData(), let image = BMImage(data: data) {
                ShareSheet(activityItems: [image])
            }
        }
        #endif
    }

    private func renderImage() {
        renderedImage = try? MermaidImageRenderer.render(code, theme: theme, scale: renderScale)
    }

    #if os(macOS)
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
    #endif

    #if os(iOS)
    private func renderPNGData() throws -> Data? {
        let renderer = MermaidImageRenderer()
        renderer.theme = theme
        renderer.scale = 2.0
        return try renderer.renderPNG(from: code)
    }

    struct ShareSheet: UIViewControllerRepresentable {
        let activityItems: [Any]

        func makeUIViewController(context: Context) -> UIActivityViewController {
            UIActivityViewController(activityItems: activityItems, applicationActivities: nil)
        }

        func updateUIViewController(_ uiViewController: UIActivityViewController, context: Context) {}
    }
    #endif
}
