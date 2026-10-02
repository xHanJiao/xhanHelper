import SwiftUI

#if os(macOS) && DEBUG

/// 窗口底板（全窗唯一那层玻璃）调参预览：Xcode 画布（⌘⌥P）里扫材质档 + 暖 tint 透明度，
/// 挑出「壁纸能透、正文看得清」的组合；选定后把值写进 GlassWindowModifier。
/// 仅 DEBUG 展示用，不参与真实布局、不接真 VM。
struct WindowBasePreview: View {
    enum Wallpaper: String, CaseIterable, Identifiable {
        case busy = "花哨彩图"
        case dark = "深色"
        case light = "浅色"
        case plain = "纯色"
        var id: String { rawValue }
    }

    enum BaseMaterial: String, CaseIterable, Identifiable {
        case ultraThin = "ultraThin"
        case thin = "thin"
        case regular = "regular"
        case thick = "thick"
        var id: String { rawValue }
        var material: Material {
            switch self {
            case .ultraThin: return .ultraThinMaterial
            case .thin: return .thinMaterial
            case .regular: return .regularMaterial
            case .thick: return .thickMaterial
            }
        }
    }

    enum Appearance: String, CaseIterable, Identifiable {
        case light = "浅色"
        case dark = "深色"
        var id: String { rawValue }
        var scheme: ColorScheme { self == .dark ? .dark : .light }
    }

    @State private var wallpaper: Wallpaper
    @State private var baseMaterial: BaseMaterial
    @State private var tint: Double
    @State private var appearance: Appearance

    init(wallpaper: Wallpaper = .busy, material: BaseMaterial = .regular,
         tint: Double = 0, appearance: Appearance = .light) {
        _wallpaper = State(initialValue: wallpaper)
        _baseMaterial = State(initialValue: material)
        _tint = State(initialValue: tint)
        _appearance = State(initialValue: appearance)
    }

    private var ink: Color { Color("assistantText") }
    private var warmTint: Color {
        appearance == .dark ? Color("chatBackgroundDeep") : Color("chatBackground")
    }

    var body: some View {
        ZStack {
            wallpaperView
            Rectangle().fill(baseMaterial.material)
            warmTint.opacity(tint)
            contentSample
        }
        .frame(width: 1340, height: 900)
        .environment(\.colorScheme, appearance.scheme)
        .overlay(alignment: .bottom) { controlStrip }
    }

    // MARK: - 模拟壁纸（画布材质渲染依赖底，这里放高对比底以判最坏情况）

    @ViewBuilder
    private var wallpaperView: some View {
        switch wallpaper {
        case .busy:
            ZStack {
                LinearGradient(colors: [.orange, .pink, .indigo, .teal, .yellow],
                               startPoint: .topLeading, endPoint: .bottomTrailing)
                Circle().fill(.white).frame(width: 360).offset(x: -360, y: -240).blur(radius: 30)
                Circle().fill(.black).frame(width: 300).offset(x: 420, y: 220).blur(radius: 40)
                Circle().fill(.red).frame(width: 240).offset(x: 300, y: -300).blur(radius: 24)
                Circle().fill(.green).frame(width: 260).offset(x: -260, y: 300).blur(radius: 24)
            }
        case .dark:
            LinearGradient(colors: [Color(white: 0.05), Color(white: 0.22)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
        case .light:
            LinearGradient(colors: [Color(white: 0.92), Color(white: 1.0)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
        case .plain:
            Color(white: 0.5)
        }
    }

    // MARK: - 代表性内容（判正文/次级文字在底板上的对比度）

    private var contentSample: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 10) {
                Text("关于特斯拉的笔记")
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(ink)
                Spacer()
                ForEach(["globe", "doc.text", "creditcard", "chart.bar", "brain", "gearshape"], id: \.self) { icon in
                    Image(systemName: icon)
                        .font(.system(size: 13))
                        .foregroundStyle(ink.opacity(0.7))
                }
            }

            VStack(alignment: .leading, spacing: 8) {
                Text("这是一段直接压在底板上的正文，用来判断对比度是否足够。AI 回复、侧栏列表、顶栏标题都属于这一类。")
                    .font(.system(size: 15))
                    .foregroundStyle(ink)
                Text("次级说明文字：底板太透时，这行最先糊掉。")
                    .font(.system(size: 13))
                    .foregroundStyle(ink.opacity(0.6))
            }
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .strokeBorder(ink.opacity(0.12), lineWidth: 0.5))

            Spacer()

            HStack(spacing: 8) {
                Text("输入消息…")
                    .font(.system(size: 14))
                    .foregroundStyle(ink.opacity(0.55))
                Spacer()
                Image(systemName: "arrow.up.circle.fill")
                    .font(.system(size: 18))
                    .foregroundStyle(ink.opacity(0.7))
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            .background(.regularMaterial, in: Capsule())
        }
        .padding(40)
    }

    // MARK: - 控制条

    private var controlStrip: some View {
        HStack(spacing: 18) {
            Picker("壁纸", selection: $wallpaper) {
                ForEach(Wallpaper.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .frame(width: 320)

            Picker("材质", selection: $baseMaterial) {
                ForEach(BaseMaterial.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .frame(width: 360)

            Picker("外观", selection: $appearance) {
                ForEach(Appearance.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .frame(width: 150)

            HStack(spacing: 6) {
                Text("tint \(String(format: "%.2f", tint))")
                    .font(.system(size: 12, design: .monospaced))
                Slider(value: $tint, in: 0...0.8, step: 0.05)
                    .frame(width: 150)
            }
        }
        .padding(12)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .padding(.bottom, 16)
    }
}

#Preview("花哨 · regular（生产）") {
    WindowBasePreview()
}

#Preview("花哨 · thick") {
    WindowBasePreview(material: .thick)
}

#Preview("深色 · thin + tint0.35") {
    WindowBasePreview(wallpaper: .dark, material: .thin, tint: 0.35, appearance: .dark)
}

#endif
