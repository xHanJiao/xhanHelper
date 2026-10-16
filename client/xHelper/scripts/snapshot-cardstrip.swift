// 卡片条（ArtifactCardStrip）离屏渲染自检 —— 把**真视图**渲成 PNG，看排布/收放/配色。
//
// 为什么用独立 swiftc 靶而不是 SPM target：这些视图要引 SwiftUI 与 asset catalog 里的 token 色，
// 而 SPM 的 target 之间文件不能重复归属（视图文件已被 app 用）。跟 `snapshot-reader.swift` 同款路子。
//
// 为什么用 NSHostingView 而不是 ImageRenderer：**ImageRenderer 不渲染 ScrollView**（展开态会是空白，
// 踩过）。NSHostingView 走真实视图树 + cacheDisplay，滚动区就是用户看到的那一段。
//
// 用法：client/xHelper/scripts/snapshot-cardstrip.sh [输出目录] [light|dark|both]
//       默认 $TMPDIR/cardstrip-snapshot + both。
//
// 只信**布局 / token**：离屏渲染的材质（.ultraThinMaterial 等）不可信；这里的卡片是纯色 + 描边，可信。

import SwiftUI
import AppKit

// MARK: - 假数据（别把真实文档内容放进仓库）

private let mockRefs: [DocumentRef] = [
    DocumentRef(
        id: "1",
        title: "リオグランデ西新宿 · 入住与落地指南",
        preview: "**10/17 入住（15:00 后）— 10/21 退房，房间 102。** 现场照着做就行。过程里两处没硬编：退房时间手册里确实没写。"
    ),
    DocumentRef(
        id: "2",
        title: "东京行程 · 落地动线",
        preview: "成田 → 新宿：N'EX 直达，出站东口步行 7 分钟。行李多就先在 B1 寄存柜放下再走。"
    ),
    DocumentRef(
        id: "3",
        title: "设备操作页",
        preview: "空调 / 热水器遥控说明。设备操作页没收，只留一句「不会用问房东」。"
    ),
    DocumentRef(
        id: "4",
        title: "周边吃饭清单",
        preview: "出站南口三家：一家荞麦、一家烧鸟、一家 24h 定食。深夜只有定食还开。"
    ),
    DocumentRef(
        id: "5",
        title: "交通卡与充值",
        preview: "Suica 在改札口旁边的机器上充值，只收现金；便利店也能充。"
    ),
    DocumentRef(
        id: "6",
        title: "退税备忘",
        preview: "同一家店当天满 5000 日元才能退；护照必须随身，别放酒店。"
    ),
    DocumentRef(
        id: "7",
        title: "紧急联络",
        preview: "房东手机 / 报警 110 / 火警 119。出事先拍现场照片。"
    ),
    DocumentRef(
        id: "8",
        title: "行李寄存与转运",
        preview: "黑猫宅急便：酒店前台填单，隔天到机场，最大 25kg 一件。"
    ),
]

/// 一张图：卡片条 + 下面一行助手正文（给出聊天里的真实观感）
private struct ChatShot: View {
    let refs: [DocumentRef]
    let width: CGFloat
    @State private var expanded: Bool

    init(refs: [DocumentRef], width: CGFloat, expanded: Bool) {
        self.refs = refs
        self.width = width
        _expanded = State(initialValue: expanded)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            if refs.count == 1 {
                // 单卡：走原来的样子（不经条子）
                DocumentRefCard(ref: refs[0], onOpen: {})
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                ArtifactCardStrip(items: refs, expanded: $expanded) { ref in
                    DocumentRefCard(ref: ref, width: 240, height: 92, onOpen: {})
                }
            }
            Text("写好了：《リオグランデ西新宿 · 入住与落地指南》，七节按落地动线排，9 张图都嵌进去了。")
                .font(.system(size: 13.5))
                .foregroundColor(Color("assistantText"))
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, 38)
        .padding(.vertical, 22)
        .frame(width: width, alignment: .leading)
        .background(Color("chatBackground"))
    }
}

// MARK: - 出图

private struct Shot {
    let name: String
    let refs: [DocumentRef]
    let width: CGFloat
    let expanded: Bool
    let dark: Bool
}

@MainActor
private func render(_ shot: Shot, to dir: URL) {
    let content = ChatShot(refs: shot.refs, width: shot.width, expanded: shot.expanded)
        .environment(\.colorScheme, shot.dark ? .dark : .light)

    let host = NSHostingView(rootView: content)
    host.appearance = NSAppearance(named: shot.dark ? .darkAqua : .aqua)
    let size = host.fittingSize
    host.frame = NSRect(origin: .zero, size: size)
    host.layoutSubtreeIfNeeded()

    // 2x 位图：默认 cacheDisplay 的 rep 是 1x，字会糊
    let scale: CGFloat = 2
    guard let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    ) else {
        FileHandle.standardError.write(Data("建位图失败：\(shot.name)\n".utf8))
        return
    }
    rep.size = size
    host.cacheDisplay(in: host.bounds, to: rep)

    guard let png = rep.representation(using: .png, properties: [:]) else {
        FileHandle.standardError.write(Data("编码失败：\(shot.name)\n".utf8))
        return
    }
    let url = dir.appendingPathComponent("\(shot.name).png")
    do {
        try png.write(to: url)
        print("✓ \(url.path)  \(Int(size.width))×\(Int(size.height))")
    } catch {
        FileHandle.standardError.write(Data("写盘失败 \(shot.name): \(error)\n".utf8))
    }
}

@MainActor
private func run(outDir: URL, mode: String) {
    do {
        try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
    } catch {
        FileHandle.standardError.write(Data("建目录失败: \(error)\n".utf8))
        return
    }

    let three = Array(mockRefs.prefix(3))
    let four = Array(mockRefs.prefix(4))
    let eight = mockRefs

    var shots: [Shot] = []
    if mode == "light" || mode == "both" {
        shots += [
            Shot(name: "01-单张-620", refs: [mockRefs[0]], width: 620, expanded: false, dark: false),
            Shot(name: "02-三张-收起-620", refs: three, width: 620, expanded: false, dark: false),
            Shot(name: "03-三张-展开-620-横向可滑(无收起控件)", refs: three, width: 620, expanded: true, dark: false),
            Shot(name: "04-四张-收起-420窄", refs: four, width: 420, expanded: false, dark: false),
            // 复现「只剩一个 ＋N 格、一张卡都没有」那个宽度（可用宽 ≈ 280 = 356 - 2*38）
            Shot(name: "05-四张-收起-356超窄-至少一张卡", refs: four, width: 356, expanded: false, dark: false),
            Shot(name: "06-四张-收起-900", refs: four, width: 900, expanded: false, dark: false),
            Shot(name: "07-八张-收起-900", refs: eight, width: 900, expanded: false, dark: false),
            Shot(name: "08-八张-收起-620", refs: eight, width: 620, expanded: false, dark: false),
        ]
    }
    if mode == "dark" || mode == "both" {
        shots += [
            Shot(name: "09-三张-收起-620-深色", refs: three, width: 620, expanded: false, dark: true),
            Shot(name: "10-三张-展开-620-深色", refs: three, width: 620, expanded: true, dark: true),
        ]
    }

    for shot in shots { render(shot, to: outDir) }
}

// MARK: - main

let args = CommandLine.arguments
let outDir = URL(fileURLWithPath: args.count > 1 ? args[1] : (NSTemporaryDirectory() + "cardstrip-snapshot"))
let mode = args.count > 2 ? args[2] : "both"
MainActor.assumeIsolated { run(outDir: outDir, mode: mode) }
