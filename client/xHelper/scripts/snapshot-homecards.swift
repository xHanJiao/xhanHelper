// 右栏首页入口卡离屏渲染自检 —— 把**真视图**（HomeEntryCard / HomeInfoRowView）渲成 PNG，
// 用来定「紧凑信息档」的卡宽下限与行数，不靠 HTML 预览猜像素。
//
// 跟 `snapshot-cardstrip.sh` 同款路子：actool 编一份 Assets.car 塞进最小 .app 再跑
// （Color("appAccent") 这类 token 靠 asset catalog 解析，裸可执行文件会渲成透明）。
//
// 用法：client/xHelper/scripts/snapshot-homecards.sh [输出目录]
//
// 只信**布局 / token**：这是纯色 + 描边的卡，可信；窗口底图（照片 backdrop）不在这里复现，
// 用 chatBackground 近似，别拿它评透明度。

import SwiftUI
import AppKit

// MARK: - 依赖桩
//
// 真 `FaviconStore` 拖着 LogBuffer / LocalPaths 一整串依赖，这里只用得到签名 → 桩掉。
// 取不到图标就走首字色块（跟线上 favicon 拿不到时同一个兜底），布局结论不受影响。
typealias FaviconImage = NSImage

final class FaviconStore: ObservableObject {
    static let shared = FaviconStore()
    func image(forHost host: String) -> FaviconImage? { nil }
}

// MARK: - 假数据（照用户真实首页的形状，别把真实内容放进仓库）

private struct CardSpec {
    let asset: String
    let title: String
    let primary: String
    let secondary: String?
    let tint: String
    let rows: [(title: String, detail: String, icon: HomeEntryCard.InfoIcon)]

    func view(layout: HomeCardLayout, width: CGFloat, height: CGFloat) -> HomeEntryCard {
        let infoRows = rows.map { row in
            HomeEntryCard.InfoRow(id: row.title, title: row.title, detail: row.detail, icon: row.icon) {}
        }
        return HomeEntryCard(
            asset: asset,
            title: title,
            primary: primary,
            secondary: secondary,
            tint: Color(tint),
            layout: layout,
            width: width,
            height: height,
            infoRows: infoRows
        ) {}
    }
}

private let specs: [CardSpec] = [
    CardSpec(asset: "sidebar_browser", title: "浏览器", primary: "1 个标签", secondary: nil,
             tint: "accentSecondary",
             // 标签行不显示 host（用户 2026-11-13）→ detail 传空
             rows: [("(17 封私信 / 37 条消息) 首页 - 知乎", "", .monogram("知"))]),
    CardSpec(asset: "sidebar_bills", title: "账单", primary: "本月 ¥3,619", secondary: "12 笔",
             tint: "assistantText",
             rows: [("打包气泡膜", "¥17", .symbol("basket")),
                    ("子午路张记", "¥58", .symbol("fork.knife")),
                    ("蜂蜜柠檬水", "¥12", .symbol("fork.knife"))]),
    CardSpec(asset: "sidebar_reminder", title: "备忘", primary: "14 条备忘", secondary: "下一条 11月15日 20:00",
             tint: "appAccent",
             rows: [("补记 10-18 → 餐饮, hx", "24 天前", .symbol("checkmark.circle")),
                    ("采集上海 9 月份脉冲能否持续", "24 天前", .symbol("checkmark.circle")),
                    ("10 月决算 +…/西安押一付三", "16 天前", .warning("exclamationmark.circle"))]),
    CardSpec(asset: "sidebar_document", title: "文档", primary: "254 篇", secondary: "最近编辑 3 小时前",
             tint: "appAccent",
             rows: [("睡眠台账 v2（2026-10-25 起）", "3 小时前", .monogram("睡")),
                    ("搬家开销台账（北京 → 西安）", "昨天", .monogram("搬")),
                    ("关于从前的梦", "2 天前", .monogram("关"))]),
    CardSpec(asset: "sidebar_artifact", title: "灵阅", primary: "45 页", secondary: "最近 昨天",
             tint: "appAccent",
             rows: [("20261112 · 从早到晚大干一整天", "昨天", .monogram("2")),
                    ("20261111 · 最长的一个双十一", "2 天前", .monogram("2")),
                    ("装箱分堆 · 一页看懂", "2 天前", .monogram("装"))]),
    CardSpec(asset: "sidebar_events", title: "事件", primary: "26 个事件", secondary: "最近更新 2 小时前",
             tint: "appAccent",
             rows: [("驾照考取（科目三/科目四）", "1 小时前", .monogram("驾")),
                    ("离京回西安…与定居规划", "2 小时前", .monogram("离")),
                    ("小安泽来京…医院心脏手术", "2 天前", .monogram("小"))]),
]

// MARK: - 一张图 = 一次档位对比（两列，跟真实首页同构）

struct Sheet: View {
    let title: String
    let subtitle: String
    let width: CGFloat
    let height: CGFloat
    let layout: HomeCardLayout

    private let gap: CGFloat = 12
    private let pagePadding: CGFloat = 26

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(Color("assistantText"))
                Text(subtitle)
                    .font(.system(size: 11))
                    .foregroundColor(Color("assistantText").opacity(0.55))
            }

            LazyVGrid(columns: [GridItem(.fixed(width), spacing: gap),
                                GridItem(.fixed(width), spacing: gap)],
                      spacing: gap) {
                ForEach(Array(specs.enumerated()), id: \.offset) { _, spec in
                    spec.view(layout: layout, width: width, height: height)
                }
            }
        }
        .padding(pagePadding)
        .frame(width: width * 2 + gap + pagePadding * 2, alignment: .leading)
        .background(Color("chatBackground"))
        .environment(\.colorScheme, .light)
    }
}


// MARK: - 问候区对比：同一张图里问候区 + 第一排卡，用来看「头部大小 vs 卡」的关系

struct GreetingSheet: View {
    let title: String
    let subtitle: String
    let cardWidth: CGFloat
    let titleSize: CGFloat
    let statsSize: CGFloat
    let spacing: CGFloat

    private let gap: CGFloat = 12
    private let pagePadding: CGFloat = 26

    private var contentWidth: CGFloat { cardWidth * 2 + gap }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(Color("assistantText"))
                Text(subtitle)
                    .font(.system(size: 11))
                    .foregroundColor(Color("assistantText").opacity(0.55))
            }

            // 跟 home 同构：垂直 18 + 问候区 + 上间距 16 + 两列卡
            VStack(alignment: .leading, spacing: 0) {
                HomeGreetingView(now: Date(timeIntervalSince1970: 1_763_000_000),
                                 pendingReminders: 14, events: 26, monthSpend: 3619,
                                 titleSize: titleSize, statsSize: statsSize, spacing: spacing)
                    .padding(.horizontal, 4)
                LazyVGrid(columns: [GridItem(.fixed(cardWidth), spacing: gap),
                                    GridItem(.fixed(cardWidth), spacing: gap)],
                          spacing: gap) {
                    ForEach(Array(specs.prefix(2).enumerated()), id: \.offset) { _, spec in
                        spec.view(layout: .info, width: cardWidth, height: 164)
                    }
                }
                .padding(.top, 16)
            }
            .padding(.vertical, 18)
        }
        .padding(pagePadding)
        .frame(width: contentWidth + pagePadding * 2, alignment: .leading)
        .background(Color("chatBackground"))
        .environment(\.colorScheme, .light)
    }
}

struct GreetingShot {
    let name: String
    let title: String
    let subtitle: String
    let titleSize: CGFloat
    let statsSize: CGFloat
    let spacing: CGFloat
}

private func renderGreeting(_ shot: GreetingShot, to dir: URL) -> Bool {
    let sheet = GreetingSheet(title: shot.title, subtitle: shot.subtitle, cardWidth: 654,
                              titleSize: shot.titleSize, statsSize: shot.statsSize,
                              spacing: shot.spacing)
    let host = NSHostingView(rootView: sheet)
    host.appearance = NSAppearance(named: .aqua)
    let size = host.fittingSize
    host.frame = NSRect(origin: .zero, size: size)
    host.layoutSubtreeIfNeeded()

    let scale: CGFloat = 2
    guard let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    ) else {
        FileHandle.standardError.write(Data("建位图失败：\(shot.name)\n".utf8))
        return false
    }
    rep.size = size
    host.cacheDisplay(in: host.bounds, to: rep)

    guard let png = rep.representation(using: .png, properties: [:]) else { return false }
    do {
        try png.write(to: dir.appendingPathComponent("\(shot.name).png"))
        print("✓ \(dir.appendingPathComponent("\(shot.name).png").path)")
        return true
    } catch {
        FileHandle.standardError.write(Data("写盘失败 \(shot.name): \(error)\n".utf8))
        return false
    }
}

// MARK: - 出图

struct Shot {
    let name: String
    let title: String
    let subtitle: String
    let width: CGFloat
    let height: CGFloat
    let layout: HomeCardLayout
}

private func render(_ shot: Shot, to dir: URL) -> Bool {
    let sheet = Sheet(title: shot.title, subtitle: shot.subtitle,
                      width: shot.width, height: shot.height, layout: shot.layout)

    let host = NSHostingView(rootView: sheet)
    host.appearance = NSAppearance(named: .aqua)
    let size = host.fittingSize
    host.frame = NSRect(origin: .zero, size: size)
    host.layoutSubtreeIfNeeded()

    let scale: CGFloat = 2
    guard size.width > 0, size.height > 0, let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: Int(size.width * scale), pixelsHigh: Int(size.height * scale),
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    ) else {
        FileHandle.standardError.write(Data("建位图失败：\(shot.name)\n".utf8))
        return false
    }
    rep.size = size
    host.cacheDisplay(in: host.bounds, to: rep)

    guard let png = rep.representation(using: .png, properties: [:]) else {
        FileHandle.standardError.write(Data("编码失败：\(shot.name)\n".utf8))
        return false
    }
    let url = dir.appendingPathComponent("\(shot.name).png")
    do {
        try png.write(to: url)
        print("✓ \(url.path)  \(Int(size.width))×\(Int(size.height))")
        return true
    } catch {
        FileHandle.standardError.write(Data("写盘失败 \(shot.name): \(error)\n".utf8))
        return false
    }
}

// MARK: - main

let outDir = URL(fileURLWithPath: CommandLine.arguments.count > 1
                 ? CommandLine.arguments[1]
                 : (NSTemporaryDirectory() + "homecards-snapshot"))

do {
    try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
} catch {
    FileHandle.standardError.write(Data("建目录失败: \(error)\n".utf8))
    exit(1)
}

let shots: [Shot] = [
    Shot(name: "01-340-紧凑3行", title: "卡宽 340 · 紧凑信息档（3 行 / 164）", subtitle: "下限：行里标题约 7 字",
         width: 340, height: 164, layout: .compactInfo),
    Shot(name: "02-380-紧凑3行", title: "卡宽 380 · 紧凑信息档（3 行 / 164）", subtitle: "参照：多 40pt 更从容",
         width: 380, height: 164, layout: .compactInfo),
    Shot(name: "03-440-紧凑3行", title: "卡宽 440 · 紧凑信息档（3 行 / 164）", subtitle: "与信息档同行数，只换排布",
         width: 440, height: 164, layout: .compactInfo),
    Shot(name: "04-520-紧凑3行", title: "卡宽 520 · 紧凑信息档（3 行 / 164）", subtitle: "≈ 用户截图的宽度",
         width: 520, height: 164, layout: .compactInfo),
    Shot(name: "05-560-信息档", title: "卡宽 560 · 信息档（3 行三列 / 164）", subtitle: "阈值本身：行区 244",
         width: 560, height: 164, layout: .info),
    Shot(name: "06-654-信息档", title: "卡宽 654 · 信息档（3 行三列 / 164）", subtitle: "宽屏：行区 320",
         width: 654, height: 164, layout: .info),
]

let greetingShots: [GreetingShot] = [
    GreetingShot(name: "问候-01-旧", title: "问候区 19 / 12.5 / 间距 6（旧）", subtitle: "卡宽 654，下同",
                 titleSize: 19, statsSize: 12.5, spacing: 6),
    GreetingShot(name: "问候-02-轻微", title: "问候区 21 / 13 / 间距 7", subtitle: "只抬一点",
                 titleSize: 21, statsSize: 13, spacing: 7),
    GreetingShot(name: "问候-03-选中", title: "问候区 23 / 13 / 间距 8（已采用）", subtitle: "头部成为视觉锚点",
                 titleSize: 23, statsSize: 13, spacing: 8),
    GreetingShot(name: "问候-04-大", title: "问候区 25 / 13.5 / 间距 9", subtitle: "再大就可能压住卡",
                 titleSize: 25, statsSize: 13.5, spacing: 9),
]

let ok = MainActor.assumeIsolated { () -> Bool in
    var ok = true
    for shot in shots where !render(shot, to: outDir) { ok = false }
    for shot in greetingShots where !renderGreeting(shot, to: outDir) { ok = false }
    return ok
}
print("截图目录: \(outDir.path)")
exit(ok ? 0 : 1)
