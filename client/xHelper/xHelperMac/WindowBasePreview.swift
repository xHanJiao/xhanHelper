import SwiftUI

#if os(macOS) && DEBUG

/// 窗口底座调参预览：Xcode 画布（⌘⌥P）里扫「底图 + 遮罩」的组合。
/// 支持真实照片底图（走生产同款 `ImageCompressor.thumbnail(maxPixel:192)`），
/// 用来定遮罩深度（写成 `color-mix(in srgb, … N%)` 的 N）与照片模糊半径。
///
/// 注：D10 已于 2026-10-07 反转——生产**不再**在整窗压 `regularMaterial`（灰闪，见 `docs/bugs/030`），
/// 故此处「全窗材质」开关仅作历史对照/诊断，不再是生产参数。
/// 仅 DEBUG 展示用，不参与真实布局、不接真 VM。
struct WindowBasePreview: View {
    enum Wallpaper: String, CaseIterable, Identifiable {
        case photo = "真实照片"
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
    /// 全窗顶层是否画 `regularMaterial`（历史对照：D10 反转前的方案，现生产不画）
    @State private var showMaterial: Bool
    /// 遮罩不透明度（照片之上的暖色 veil；用来定 color-mix 的 N）
    @State private var veil: Double
    /// 中性白霜不透明度（补偿移除 material 后丢失的提亮；生产取 AppBackdrop.frost）
    @State private var frost: Double
    /// 照片档的额外模糊（192 缩略图放大后本身已很糊，默认只加一点）
    @State private var blur: Double
    @State private var appearance: Appearance

    init(wallpaper: Wallpaper = .photo, material: BaseMaterial = .regular,
         showMaterial: Bool = true, veil: Double = 0.40, frost: Double = 0.40, blur: Double = 60,
         appearance: Appearance = .light) {
        _wallpaper = State(initialValue: wallpaper)
        _baseMaterial = State(initialValue: material)
        _showMaterial = State(initialValue: showMaterial)
        _veil = State(initialValue: veil)
        _frost = State(initialValue: frost)
        _blur = State(initialValue: blur)
        _appearance = State(initialValue: appearance)
    }

    private var ink: Color { Color("assistantText") }
    private var warmTint: Color {
        appearance == .dark ? Color("chatBackgroundDeep") : Color("chatBackground")
    }

    var body: some View {
        ZStack {
            wallpaperView
            if showMaterial {
                Rectangle().fill(baseMaterial.material)
            }
            warmTint.opacity(veil)
            Color.white.opacity(frost)
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
        case .photo:
            photoWallpaper
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

    /// 真实照片底图：与生产 `AppBackdrop` 同一路径——包内 192px 默认图放大铺满。
    /// 读包内资源（`bgDefault`），不读 `~/Downloads`（TCC 保护目录，预览/沙箱进程读不到）。
    private var photoWallpaper: some View {
        Image("bgDefault")
            .resizable()
            .interpolation(.high)
            .aspectRatio(contentMode: .fill)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .blur(radius: blur, opaque: true)   // opaque: 重模糊时四边不渐隐（否则透出底层）
            .clipped()
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
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
            .background(.ultraThinMaterial, in: Capsule())

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
        VStack(spacing: 8) {
            HStack(spacing: 18) {
                Picker("壁纸", selection: $wallpaper) {
                    ForEach(Wallpaper.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .frame(width: 420)

                Picker("材质", selection: $baseMaterial) {
                    ForEach(BaseMaterial.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .frame(width: 300)
                .disabled(!showMaterial)

                Toggle("全窗材质", isOn: $showMaterial)
                    .toggleStyle(.switch)

                Picker("外观", selection: $appearance) {
                    ForEach(Appearance.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .frame(width: 130)
            }

            HStack(spacing: 18) {
                sliderRow(title: "遮罩 \(String(format: "%.2f", veil))", value: $veil, range: 0...0.8)
                sliderRow(title: "白霜 \(String(format: "%.2f", frost))", value: $frost, range: 0...0.5)
                sliderRow(title: "模糊 \(Int(blur))", value: $blur, range: 0...160)
            }
        }
        .padding(12)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .padding(.bottom, 16)
    }

    private func sliderRow(title: String, value: Binding<Double>, range: ClosedRange<Double>) -> some View {
        HStack(spacing: 6) {
            Text(title)
                .font(.system(size: 12, design: .monospaced))
                .frame(width: 110, alignment: .leading)
            Slider(value: value, in: range)
                .frame(width: 200)
        }
    }
}

#Preview("照片 · 带材质(历史) · 浅色") {
    WindowBasePreview()
}

#Preview("照片 · 带材质(历史) · 深色") {
    WindowBasePreview(wallpaper: .photo, showMaterial: true, appearance: .dark)
}

#Preview("照片 · 纯遮罩(生产) · 浅色") {
    WindowBasePreview(wallpaper: .photo, showMaterial: false, veil: 0.35)
}

#Preview("照片 · 纯遮罩(生产) · 深色") {
    WindowBasePreview(wallpaper: .photo, showMaterial: false, veil: 0.35, appearance: .dark)
}

#Preview("对比 · 花哨 + 材质(历史)") {
    WindowBasePreview(wallpaper: .busy)
}

#endif
