// 「全部关闭」按钮候选原型 —— 真 SwiftUI 离屏渲成 PNG（不做 HTML 预览）。
//
// 只信**布局 / 图标语义 / token**：这里背景是近似的底图色块，最终观感以 Xcode 真机为准。
//
// 用法：client/xHelper/scripts/snapshot-closeall.sh [输出目录]

import SwiftUI
import AppKit

// MARK: - token（与 App 代码同款算法：primary 透明度 + 白描边）

private struct Pal {
    let dark: Bool

    /// 右栏这块坐在窗口底图上：浅色是偏蓝灰的模糊照片，深色近黑
    var backdrop: Color {
        dark ? Color(.sRGB, red: 0.11, green: 0.11, blue: 0.12, opacity: 1)
             : Color(.sRGB, red: 0.78, green: 0.82, blue: 0.85, opacity: 1)
    }
    var backdropBottom: Color {
        dark ? Color(.sRGB, red: 0.09, green: 0.09, blue: 0.10, opacity: 1)
             : Color(.sRGB, red: 0.84, green: 0.87, blue: 0.89, opacity: 1)
    }
    /// 浅色 primary = 黑，深色 primary = 白
    var primary: Color { dark ? .white : .black }
    var accent: Color {
        dark ? Color(.sRGB, red: 0.573, green: 0.784, blue: 0.706, opacity: 1)
             : Color(.sRGB, red: 0.184, green: 0.490, blue: 0.388, opacity: 1)
    }

    func chipBg(active: Bool) -> Color { primary.opacity(active ? 0.14 : 0.05) }
    func chipStroke(active: Bool) -> Color { Color.white.opacity(active ? 0.28 : 0.16) }
}

// MARK: - 组件（照抄 RightSidebarTabStrip 的实际尺寸/圆角）

private struct HomeChip: View {
    let p: Pal
    let active = true

    var body: some View {
        Image(systemName: "house")
            .font(.system(size: 11, weight: .medium))
            .foregroundStyle(active ? p.accent : Color.secondary)
            .frame(width: 26, height: 26)
            .background(p.chipBg(active: active), in: Capsule())
            .overlay(Capsule().stroke(p.chipStroke(active: active), lineWidth: 0.5))
    }
}

private struct TabChip: View {
    let p: Pal
    let title: String

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: "globe")
                .font(.system(size: 10.5, weight: .medium))
                .foregroundStyle(.secondary)
            Text(title)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(.horizontal, 9)
        .frame(height: 26)
        .background(p.chipBg(active: false), in: RoundedRectangle(cornerRadius: 13, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 13, style: .continuous)
            .stroke(p.chipStroke(active: false), lineWidth: 0.5))
    }
}

/// 候选外形：圆底图标 / 无底图标 / 文字 chip
private struct RoundIcon: View {
    let p: Pal
    let symbol: String
    /// nil = 无底（只在 hover 时给淡底）
    let background: Color?

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: 11, weight: .medium))
            .foregroundStyle(.secondary)
            .frame(width: 26, height: 26)
            .background(background ?? .clear, in: Capsule())
            .overlay(Capsule().stroke(background == nil ? .clear : p.chipStroke(active: false), lineWidth: 0.5))
    }
}

private struct TextChip: View {
    let p: Pal
    let text: String
    let filled: Bool

    var body: some View {
        Text(text)
            .font(.system(size: 11, weight: .medium))
            .foregroundStyle(filled ? Color.secondary : Color.secondary)
            .padding(.horizontal, 9)
            .frame(height: 26)
            .background(filled ? p.chipBg(active: false) : .clear,
                        in: RoundedRectangle(cornerRadius: 13, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 13, style: .continuous)
                .stroke(filled ? p.chipStroke(active: false) : .clear, lineWidth: 0.5))
    }
}

// MARK: - 候选清单

private struct Candidate: Identifiable {
    let id: String
    let title: String
    let note: String
    /// true = 画在 tab 行末尾（而不是首页右侧）
    let trailing: Bool
    let make: (Pal) -> AnyView
}

private let candidates: [Candidate] = [
    Candidate(id: "A", title: "A · 圆底 + 叠层减号（现方案）",
              note: "圆底里放矩形叠层，语义=关掉所有标签", trailing: false,
              make: { p in AnyView(RoundIcon(p: p, symbol: "rectangle.stack.badge.minus",
                                             background: p.chipBg(active: false))) }),
    Candidate(id: "B", title: "B · 圆底 + 纯叉",
              note: "去掉 xmark.circle 的同心圆，只剩一个叉", trailing: false,
              make: { p in AnyView(RoundIcon(p: p, symbol: "xmark",
                                             background: p.chipBg(active: false))) }),
    Candidate(id: "C", title: "C · 无底裸图标（hover 才出底）",
              note: "最轻，平时不占视觉重量；hover 时给 8% 淡底", trailing: false,
              make: { p in AnyView(ZStack {
                  RoundIcon(p: p, symbol: "rectangle.stack.badge.minus", background: nil)
                  RoundIcon(p: p, symbol: "rectangle.stack.badge.minus",
                            background: p.primary.opacity(0.08)).opacity(0.0)
              }) }),
    Candidate(id: "D", title: "D · 圆底 + 垃圾桶",
              note: "最直白但偏\"删除文件\"，不像关标签", trailing: false,
              make: { p in AnyView(RoundIcon(p: p, symbol: "trash",
                                             background: p.chipBg(active: false))) }),
    Candidate(id: "E", title: "E · 文字 chip「全部关闭」",
              note: "零歧义，但标签多了会挤；窄宽下要占 40pt", trailing: false,
              make: { p in AnyView(TextChip(p: p, text: "全部关闭", filled: true)) }),
    Candidate(id: "F", title: "F · 放行尾（原来 + 的位置）",
              note: "左边只留首页与标签，工具类动作归到右端", trailing: true,
              make: { p in AnyView(RoundIcon(p: p, symbol: "rectangle.stack.badge.minus",
                                             background: p.chipBg(active: false))) }),
    Candidate(id: "G", title: "G · 无底裸叉",
              note: "最轻 + 最直白；和 B 的差别只在底", trailing: false,
              make: { p in AnyView(RoundIcon(p: p, symbol: "xmark", background: nil)) }),
]

// MARK: - 一屏

private struct Sheet: View {
    let p: Pal

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(p.dark ? "深色" : "浅色")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(.secondary)
                .padding(.bottom, 10)

            ForEach(candidates) { c in
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 6) {
                        HomeChip(p: p)
                        if !c.trailing { c.make(p) }
                        TabChip(p: p, title: "www.xiaohongshu.com")
                        TabChip(p: p, title: "文档")
                        if c.trailing {
                            Spacer(minLength: 12)
                            c.make(p)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)

                    Text(c.title)
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(.primary.opacity(0.85))
                    Text(c.note)
                        .font(.system(size: 10))
                        .foregroundStyle(.secondary)
                }
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 14)
        .frame(width: 560, alignment: .leading)
        .background(
            LinearGradient(colors: [p.backdrop, p.backdropBottom],
                           startPoint: .top, endPoint: .bottom)
        )
        .environment(\.colorScheme, p.dark ? .dark : .light)
    }
}

// MARK: - main

private func render(dark: Bool, to dir: URL) -> Bool {
    let p = Pal(dark: dark)
    let host = NSHostingView(rootView: Sheet(p: p))
    host.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
    host.frame = NSRect(x: 0, y: 0, width: 560, height: 640)
    host.layoutSubtreeIfNeeded()

    let size = host.fittingSize == .zero ? host.frame.size : NSSize(width: 560, height: host.fittingSize.height)
    host.frame = NSRect(origin: .zero, size: size)
    host.layoutSubtreeIfNeeded()

    let scale: CGFloat = 2
    guard let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    ) else { return false }
    rep.size = size
    host.cacheDisplay(in: host.bounds, to: rep)

    guard let png = rep.representation(using: .png, properties: [:]) else { return false }
    let url = dir.appendingPathComponent(dark ? "dark.png" : "light.png")
    do {
        try png.write(to: url)
        print("✓ \(url.path)")
        return true
    } catch {
        FileHandle.standardError.write(Data("写文件失败: \(error)\n".utf8))
        return false
    }
}

let outDir = URL(fileURLWithPath: CommandLine.arguments.count > 1
                 ? CommandLine.arguments[1]
                 : (NSTemporaryDirectory() + "closeall-snapshot"))
do {
    try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
} catch {
    FileHandle.standardError.write(Data("建目录失败: \(error)\n".utf8))
    exit(1)
}

var ok = true
MainActor.assumeIsolated {
    if !render(dark: false, to: outDir) { ok = false }
    if !render(dark: true, to: outDir) { ok = false }
}
exit(ok ? 0 : 1)
