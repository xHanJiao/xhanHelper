// 右栏「统一 tab」方案原型 —— 真 SwiftUI 渲成 PNG，用来定排布/信息密度，不做 HTML 预览。
//
// 只画方案里的**壳**：顶栏（会话下拉 + 当前 tab 动作胶囊 + 工具胶囊）+ 右栏顶部统一的
// 胶囊 tab 条 + 内容区（列表 / 阅读 / 词云）。不接任何真 VM，假数据只为看排布。
//
// 只信**布局 / token**：离屏渲染的材质（.ultraThinMaterial）不可信，这里一律用近似的纯色，
// 观感以 Xcode 真机为准。
//
// 用法：client/xHelper/scripts/snapshot-righttabs.sh [输出目录]

import SwiftUI
import AppKit

// MARK: - token

private func hx(_ v: UInt32, _ o: Double = 1) -> Color {
    Color(.sRGB,
          red: Double((v >> 16) & 0xFF) / 255,
          green: Double((v >> 8) & 0xFF) / 255,
          blue: Double(v & 0xFF) / 255,
          opacity: o)
}

/// 深浅两套取值，全部来自 `Assets.xcassets` 的 token（见 AGENTS.md 色板）。
private struct Pal {
    let dark: Bool

    var bgTop: Color { hx(dark ? 0x24262A : 0xF7EEDD) }
    var bgBottom: Color { hx(dark ? 0x141416 : 0xEFE2C8) }
    var text: Color { hx(dark ? 0xFFFFFF : 0x3B2E1F) }
    var text2: Color { hx(dark ? 0xFFFFFF : 0x3B2E1F, dark ? 0.62 : 0.58) }
    var text3: Color { hx(dark ? 0xFFFFFF : 0x3B2E1F, dark ? 0.38 : 0.34) }
    var accent: Color { hx(dark ? 0x92C8B4 : 0x2F7D63) }
    var accentSoft: Color { hx(dark ? 0x92C8B4 : 0x2F7D63, dark ? 0.20 : 0.14) }

    var capsule: Color { hx(0xFFFFFF, dark ? 0.10 : 0.62) }
    var capsuleStroke: Color { hx(0xFFFFFF, dark ? 0.14 : 0.75) }
    var shadow: Color { dark ? hx(0x000000, 0.45) : hx(0x000000, 0.12) }

    var chipIdle: Color { hx(0xFFFFFF, dark ? 0.03 : 0.26) }
    var chipActive: Color { hx(0xFFFFFF, dark ? 0.22 : 0.95) }
    var chipActiveStroke: Color { hx(0xFFFFFF, dark ? 0.22 : 0.95) }

    var card: Color { hx(0xFFFFFF, dark ? 0.07 : 0.52) }
    var cardStroke: Color { hx(0xFFFFFF, dark ? 0.10 : 0.70) }
    var segBg: Color { hx(0xFFFFFF, dark ? 0.07 : 0.45) }
    var divider: Color { hx(dark ? 0xFFFFFF : 0x3B2E1F, dark ? 0.10 : 0.10) }
}

// MARK: - 顶栏

private struct TopBar: View {
    let p: Pal
    /// 当前 tab 的动作图标（center 槽）。文档列表 / 详情 / 书架各自不同。
    let actions: [String]
    let highlighted: Set<String>

    var body: some View {
        HStack(spacing: 8) {
            HStack(spacing: 8) {
                light(0xFF5F57); light(0xFEBC2E); light(0x28C840)
            }
            .padding(.leading, 20)

            conversationPill
            plusCircle

            Spacer(minLength: 0)

            capsule(actions, highlighted: highlighted)
            Rectangle().fill(p.divider).frame(width: 1, height: 14).padding(.horizontal, 2)
            capsule(["arrow.up.left.and.arrow.down.right", "sidebar.trailing",
                     p.dark ? "sun.max.fill" : "moon.fill", "gearshape"], highlighted: [])
        }
        .padding(.trailing, 16)
        .frame(height: 46)
    }

    private func light(_ color: UInt32) -> some View {
        Circle().fill(hx(color)).frame(width: 12, height: 12)
    }

    private var conversationPill: some View {
        HStack(spacing: 4) {
            Text("又收两个大箱子")
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(p.text.opacity(0.85))
                .lineLimit(1)
            Image(systemName: "chevron.down")
                .font(.system(size: 10, weight: .bold))
                .foregroundStyle(p.text2)
        }
        .padding(.horizontal, 12)
        .frame(height: 28)
        .background(p.capsule, in: Capsule())
        .overlay(Capsule().stroke(p.capsuleStroke, lineWidth: 0.5))
        .shadow(color: p.shadow, radius: 4, y: 2)
    }

    private var plusCircle: some View {
        Image(systemName: "plus")
            .font(.system(size: 15, weight: .medium))
            .foregroundStyle(p.text.opacity(0.7))
            .frame(width: 28, height: 28)
            .background(p.capsule, in: Circle())
            .overlay(Circle().stroke(p.capsuleStroke, lineWidth: 0.5))
            .shadow(color: p.shadow, radius: 4, y: 2)
    }

    private func capsule(_ icons: [String], highlighted: Set<String>) -> some View {
        HStack(spacing: 4) {
            ForEach(Array(icons.enumerated()), id: \.offset) { index, icon in
                if index > 0 {
                    Rectangle().fill(p.divider).frame(width: 1, height: 14)
                }
                Image(systemName: icon)
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(highlighted.contains(icon) ? p.accent : p.text.opacity(0.7))
                    .frame(width: 26, height: 26)
            }
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 1)
        .frame(height: 28)
        .background(p.capsule, in: Capsule())
        .overlay(Capsule().stroke(p.capsuleStroke, lineWidth: 0.5))
        .shadow(color: p.shadow, radius: 4, y: 2)
    }
}

// MARK: - 统一的胶囊 tab 条

private struct Chip: Identifiable {
    let id = UUID()
    let icon: String
    let title: String
    var count: String? = nil
    var active = false
}

private struct TabStrip: View {
    let p: Pal
    let chips: [Chip]
    /// 放不下时末尾那颗 ⋯（点开是 tab 菜单）
    let overflow: Bool

    var body: some View {
        HStack(spacing: 6) {
            ForEach(chips) { chip in
                chipView(chip)
            }
            if overflow {
                Image(systemName: "ellipsis")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(p.text2)
                    .frame(width: 26, height: 24)
                    .background(p.chipIdle, in: Capsule())
                    .overlay(Capsule().stroke(hx(0xFFFFFF, p.dark ? 0.10 : 0.55), lineWidth: 0.5))
            }
            Image(systemName: "plus")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(p.text2)
                .frame(width: 26, height: 24)
                .background(p.chipIdle, in: Capsule())
                .overlay(Capsule().stroke(hx(0xFFFFFF, p.dark ? 0.10 : 0.55), lineWidth: 0.5))
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.top, 2)
        .padding(.bottom, 8)
    }

    private func chipView(_ chip: Chip) -> some View {
        HStack(spacing: 5) {
            Image(systemName: chip.icon)
                .font(.system(size: 10.5, weight: .medium))
                .foregroundStyle(chip.active ? p.accent : p.text2)
            Text(chip.title)
                .font(.system(size: 12, weight: chip.active ? .semibold : .regular))
                .foregroundStyle(chip.active ? p.text : p.text2)
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: 170, alignment: .leading)
                .fixedSize(horizontal: true, vertical: false)
            if let count = chip.count {
                Text(count)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(chip.active ? p.accent : p.text3)
            }
            if chip.active {
                // 关闭：悬停才出现，静态图只画在激活项上
                Image(systemName: "xmark")
                    .font(.system(size: 8, weight: .bold))
                    .foregroundStyle(p.text3)
            }
        }
        .padding(.leading, 9)
        .padding(.trailing, chip.active ? 7 : 9)
        .frame(height: 26)
        .background(chip.active ? p.chipActive : p.chipIdle, in: Capsule())
        .overlay(
            Capsule().stroke(chip.active ? p.chipActiveStroke : hx(0xFFFFFF, p.dark ? 0.10 : 0.55),
                             lineWidth: 0.5)
        )
        .shadow(color: chip.active ? p.shadow : .clear, radius: 3, y: 1)
    }
}

// MARK: - 内容区

private struct ModeRow: View {
    let p: Pal
    let selected: String
    let trailing: String

    var body: some View {
        HStack(spacing: 8) {
            segmented
            Spacer(minLength: 0)
            Text(trailing)
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(p.text2)
                .padding(.horizontal, 9)
                .frame(height: 22)
                .background(p.segBg, in: Capsule())
        }
        .padding(.horizontal, 14)
        .frame(height: 40)
    }

    private var segmented: some View {
        HStack(spacing: 2) {
            ForEach(["列表", "词云", "回收站"], id: \.self) { item in
                Text(item)
                    .font(.system(size: 11.5, weight: item == selected ? .semibold : .regular))
                    .foregroundStyle(item == selected ? p.text : p.text2)
                    .padding(.horizontal, 9)
                    .frame(height: 21)
                    .background(item == selected ? p.chipActive : Color.clear, in: Capsule())
            }
        }
        .padding(2)
        .background(p.segBg, in: Capsule())
    }
}

private struct DocCard: View {
    let p: Pal
    let title: String
    let summary: String
    let tags: [String]
    let time: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(p.text)
                .lineLimit(2)
            Text(summary)
                .font(.system(size: 12.5))
                .foregroundStyle(p.text2)
                .lineLimit(2)
                .lineSpacing(2)
            HStack(spacing: 6) {
                ForEach(tags, id: \.self) { tag in
                    Text(tag)
                        .font(.system(size: 10, weight: .medium))
                        .foregroundStyle(p.text2)
                        .padding(.horizontal, 7)
                        .padding(.vertical, 2)
                        .background(p.accentSoft, in: Capsule())
                }
                Spacer(minLength: 0)
                Text(time)
                    .font(.system(size: 10.5))
                    .foregroundStyle(p.text3)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(p.card, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(p.cardStroke, lineWidth: 0.5))
    }
}

private struct ListContent: View {
    let p: Pal

    var body: some View {
        VStack(spacing: 0) {
            ModeRow(p: p, selected: "列表", trailing: "成品 · 254")
            ScrollView {
                VStack(spacing: 10) {
                    DocCard(p: p, title: "睡眠台账 v2（2026-10-25 起）",
                            summary: "数据准绳：HTML 看板只是展现层，看板上的派生 KPI（均值、合计、最短/最长）一律以本页为准。",
                            tags: ["台账", "睡眠", "睡眠日志"], time: "4 小时前")
                    DocCard(p: p, title: "2026 年 11 月家庭预算拆解",
                            summary: "房租、还贷、日常开销三块分开记；这版只写口径，具体数字每周日更新。",
                            tags: ["预算", "月度"], time: "昨天")
                    DocCard(p: p, title: "驾照考试复盘（科目三）",
                            summary: "两次挂的点都在动作细节：靠边停车压线、起步忘打灯。结论是先搁置，回西安再考。",
                            tags: ["驾照", "复盘"], time: "3 天前")
                    DocCard(p: p, title: "Mac 端右栏结构笔记",
                            summary: "把面板态和网页标签态合成一套 tab 之后，返回按钮可以整条删掉，动作按钮全进顶栏。",
                            tags: ["客户端", "设计"], time: "上周")
                }
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 14)
                .padding(.bottom, 16)
            }
        }
    }
}

private struct ReaderContent: View {
    let p: Pal
    let kicker: String
    let paragraphs: [String]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Text(kicker)
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(p.text3)
                ForEach(Array(paragraphs.enumerated()), id: \.offset) { _, para in
                    Text(para)
                        .font(.system(size: 16.5))
                        .foregroundStyle(p.text.opacity(0.92))
                        .lineSpacing(9)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxWidth: 728, alignment: .leading)
            .frame(maxWidth: .infinity)
            .padding(.top, 34)
            .padding(.bottom, 48)
        }
    }
}

private struct CloudContent: View {
    let p: Pal
    private let tags: [(String, CGFloat, Bool)] = [
        ("台账", 30, true), ("睡眠", 26, true), ("看板", 15, false), ("预算", 18, false),
        ("复盘", 13, false), ("驾照", 15, false), ("客户端", 22, true), ("设计", 17, false),
        ("西安", 14, false), ("装修", 12, false), ("归档", 11, false), ("睡眠日志", 20, true),
        ("旅行", 13, false), ("工作流", 16, false), ("macOS", 12, false), ("断舍离", 11, false),
        ("周报", 12, false), ("读书", 14, false), ("日语", 11, false), ("家庭", 13, false),
    ]

    var body: some View {
        VStack(spacing: 0) {
            ModeRow(p: p, selected: "词云", trailing: "≥ 2 篇")
            ScrollView {
                VStack(spacing: 18) {
                    ForEach(0..<4, id: \.self) { row in
                        HStack(spacing: 14) {
                            ForEach(Array(tags.enumerated()).filter { $0.offset % 4 == row }, id: \.offset) { _, tag in
                                Text(tag.0)
                                    .font(.system(size: tag.1, weight: tag.2 ? .semibold : .regular))
                                    .foregroundStyle(tag.2 ? p.accent : p.text2)
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 4)
                                    .background(tag.2 ? p.accentSoft : Color.clear, in: Capsule())
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity)
                .padding(.top, 28)
                .padding(.bottom, 40)
            }
        }
    }
}

// MARK: - 整窗

private struct ShotSpec {
    let name: String
    let dark: Bool
    let chips: [Chip]
    let overflow: Bool
    let actions: [String]
    let highlighted: Set<String>
    let content: ContentKind
}

private enum ContentKind {
    case documents
    case documentDetail
    case cloud
    case bookReader
}

private struct WindowShot: View {
    let spec: ShotSpec
    private var p: Pal { Pal(dark: spec.dark) }

    var body: some View {
        VStack(spacing: 0) {
            TopBar(p: p, actions: spec.actions, highlighted: spec.highlighted)
            HStack(spacing: 0) {
                // 窗口左侧的裁切区（中栏）：只铺底图，不放假聊天内容
                backdrop.frame(width: 236)
                Rectangle().fill(p.divider).frame(width: 1)
                VStack(spacing: 0) {
                    TabStrip(p: p, chips: spec.chips, overflow: spec.overflow)
                    Rectangle().fill(p.divider).frame(height: 0.5)
                    content
                }
            }
        }
        .frame(width: 1280, height: 760)
        .background(backdrop)
        .environment(\.colorScheme, spec.dark ? .dark : .light)
    }

    @ViewBuilder
    private var content: some View {
        switch spec.content {
        case .documents:
            ListContent(p: p)
        case .documentDetail:
            ReaderContent(p: p, kicker: "文档 · 富士日记 · 昭和四十二年", paragraphs: [
                "丈夫演讲回来，钻进被窝睡得正熟，我不能把他喊起来。我们说了一圈车轱辘话，那个男的没办法，向我逐一讲述他对武田泰淳的人格的不满。",
                "田中，但像是撒谎，打来电话，用一种仿佛喝醉了的声音絮絮叨叨地谈论武田。听了几句我就明白，他说的不是作品，是人。",
                "我把听筒夹在肩上，一边应着，一边想：这些话明天大概会被他自己推翻。人一喝酒就爱给人下判断，醒来又都收回。",
            ])
        case .cloud:
            CloudContent(p: p)
        case .bookReader:
            ReaderContent(p: p, kicker: "第一七四章", paragraphs: [
                "雪停之后，屋檐下滴了一整夜的水。早上推门，院子里的脚印已经被新雪填平了一半，只剩几道浅浅的凹痕，认不出是谁的。",
                "她说今年冬天格外长。我说每年都这么说。她把茶推过来，没有接话，只是把火拨旺了一点。",
                "午后我翻到去年冬天写下的那几页，字迹被潮气洇开，勉强能读。那时候我还以为，春天一到，什么都会跟着好起来。",
            ])
        }
    }

    /// 底图：真机是照片重模糊 + 40% 遮罩 + 白霜；离屏只能近似成同色系渐变 + 几团色斑。
    private var backdrop: some View {
        ZStack {
            LinearGradient(colors: [p.bgTop, p.bgBottom], startPoint: .topLeading, endPoint: .bottomTrailing)
            RadialGradient(colors: [p.accent.opacity(spec.dark ? 0.22 : 0.16), .clear],
                           center: .topTrailing, startRadius: 20, endRadius: 620)
            RadialGradient(colors: [hx(0x41C9E2).opacity(spec.dark ? 0.12 : 0.14), .clear],
                           center: .bottomLeading, startRadius: 20, endRadius: 560)
            Color.white.opacity(spec.dark ? 0.02 : 0.16)
        }
    }
}

// MARK: - 出图

@MainActor
private func render(_ spec: ShotSpec, to dir: URL) -> Bool {
    let content = WindowShot(spec: spec)
    let host = NSHostingView(rootView: content)
    host.appearance = NSAppearance(named: spec.dark ? .darkAqua : .aqua)
    let size = NSSize(width: 1280, height: 760)
    host.frame = NSRect(origin: .zero, size: size)
    host.layoutSubtreeIfNeeded()

    let scale: CGFloat = 2
    guard let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    ) else {
        FileHandle.standardError.write(Data("建位图失败：\(spec.name)\n".utf8))
        return false
    }
    rep.size = size
    host.cacheDisplay(in: host.bounds, to: rep)

    guard let png = rep.representation(using: .png, properties: [:]) else {
        FileHandle.standardError.write(Data("编码失败：\(spec.name)\n".utf8))
        return false
    }
    let url = dir.appendingPathComponent("\(spec.name).png")
    do {
        try png.write(to: url)
        print("✓ \(url.path)")
        return true
    } catch {
        FileHandle.standardError.write(Data("写盘失败 \(spec.name): \(error)\n".utf8))
        return false
    }
}

private let specs: [ShotSpec] = [
    // 1. 列表也是 tab：文档列表激活，模式行只剩 列表/词云/回收站 + 层级，返回/标题整行已删
    ShotSpec(
        name: "01-文档列表-浅色",
        dark: false,
        chips: [
            Chip(icon: "doc.text", title: "文档", count: "254", active: true),
            Chip(icon: "books.vertical", title: "书架"),
            Chip(icon: "globe", title: "浏览器"),
            Chip(icon: "doc.richtext", title: "灵阅"),
        ],
        overflow: false,
        actions: ["arrow.clockwise", "magnifyingglass", "checkmark.circle", "tray.and.arrow.down"],
        highlighted: [],
        content: .documents
    ),
    // 2. 详情是独立 tab：列表 tab 还在条上，详情紧挨它；内容区没有任何 header
    ShotSpec(
        name: "02-文档详情-深色",
        dark: true,
        chips: [
            Chip(icon: "doc.text", title: "文档", count: "254"),
            Chip(icon: "doc.text", title: "富士日记 · 昭和四十二年", active: true),
            Chip(icon: "books.vertical", title: "书架"),
            Chip(icon: "globe", title: "浏览器"),
            Chip(icon: "doc.richtext", title: "灵阅"),
        ],
        overflow: true,
        actions: ["list.bullet.rectangle", "magnifyingglass", "arrow.down.circle", "link"],
        highlighted: ["list.bullet.rectangle"],
        content: .documentDetail
    ),
    // 3. 词云是列表 tab 的 mode，不单开 tab
    ShotSpec(
        name: "03-词云mode-深色",
        dark: true,
        chips: [
            Chip(icon: "doc.text", title: "文档", count: "254", active: true),
            Chip(icon: "books.vertical", title: "书架"),
            Chip(icon: "globe", title: "浏览器"),
            Chip(icon: "doc.richtext", title: "灵阅"),
        ],
        overflow: false,
        actions: ["arrow.clockwise", "magnifyingglass", "checkmark.circle", "tray.and.arrow.down"],
        highlighted: [],
        content: .cloud
    ),
    // 4. 书架列表 + 书阅读两个 tab 并存，书 tab 紧挨书架 tab
    ShotSpec(
        name: "04-书架与书详情-浅色",
        dark: false,
        chips: [
            Chip(icon: "doc.text", title: "文档"),
            Chip(icon: "books.vertical", title: "书架"),
            Chip(icon: "book.closed", title: "富士日记", active: true),
            Chip(icon: "globe", title: "浏览器"),
        ],
        overflow: false,
        actions: ["chevron.left", "chevron.right", "sidebar.leading", "bookmark", "magnifyingglass", "gearshape"],
        highlighted: ["bookmark"],
        content: .bookReader
    ),
]

// MARK: - main

let args = CommandLine.arguments
let outDir = URL(fileURLWithPath: args.count > 1
                 ? args[1]
                 : (NSTemporaryDirectory() + "righttabs-snapshot"))
do {
    try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
} catch {
    FileHandle.standardError.write(Data("建目录失败: \(error)\n".utf8))
    exit(1)
}

var ok = true
MainActor.assumeIsolated {
    for spec in specs where !render(spec, to: outDir) { ok = false }
}
exit(ok ? 0 : 1)
