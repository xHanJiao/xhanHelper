import SwiftUI
#if os(macOS)
import AppKit

/// 文档词云 / 记忆主题词云共用的标签云：
/// 圆盘内分行排布，频次越高字号越大且越靠中心；一屏缩放贴合，不滚动。
struct TagCloudView: View {
    let tags: [CloudTag]
    /// 本地高亮搜索词：命中词 accent 强调，其余词变淡；不改布局。
    var query: String = ""
    /// ⌘ 多选中的标签（记忆词云不用，留空即走常态分支）。
    var selected: Set<String> = []
    /// 与所选标签直接共现（1 跳）的标签。
    var neighbors: Set<String> = []
    let onTap: (String) -> Void
    var onCmdTap: (String) -> Void = { _ in }
    /// tooltip 里的计数单位（如「篇文档」「条记忆」）。
    var helpLabel: String = "项"

    @State private var placed: [PlacedTag] = []

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .topLeading) {
                ForEach(placed) { item in
                    let style = emphasis(for: item)
                    Text(item.tag.name)
                        .font(.system(size: item.fontSize, weight: item.weight))
                        .foregroundColor(style.color)
                        .opacity(style.opacity)
                        .padding(.horizontal, style.highlighted ? 4 : 0)
                        .background(
                            Capsule().fill(style.highlighted ? Color("appAccent").opacity(0.14) : .clear)
                        )
                        .fixedSize()
                        .contentShape(Rectangle())
                        .onTapGesture {
                            // 用实时修饰键状态区分单击筛选 / ⌘ 多选，避免两个手势打架
                            if NSEvent.modifierFlags.contains(.command) {
                                onCmdTap(item.tag.name)
                            } else {
                                onTap(item.tag.name)
                            }
                        }
                        .help("\(item.tag.count) \(helpLabel)")
                        .position(x: item.center.x, y: item.center.y)
                }
            }
            .frame(width: geo.size.width, height: geo.size.height)
            .onAppear { rebuild(in: geo.size) }
            .onChange(of: geo.size) { _, size in rebuild(in: size) }
            .onChange(of: tags) { _, _ in rebuild(in: geo.size) }
        }
    }

    private struct Emphasis {
        let color: Color
        let opacity: Double
        let highlighted: Bool
    }

    /// nil = 无查询（常态）；true = 命中；false = 未命中。
    private func matchState(_ name: String) -> Bool? {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !q.isEmpty else { return nil }
        return name.localizedCaseInsensitiveContains(q)
    }

    /// 高亮分层：选中 > 共现邻居 > 常态；搜索查询在每层上再压暗未命中。
    private func emphasis(for item: PlacedTag) -> Emphasis {
        let name = item.tag.name
        let q = matchState(name)
        if selected.isEmpty {
            let color = q == true ? Color("appAccent") : Color.primary
            var opacity = item.opacity
            if q == false { opacity = min(opacity, 0.2) }
            return Emphasis(color: color, opacity: opacity, highlighted: false)
        }
        if selected.contains(name) {
            return Emphasis(color: Color("appAccent"), opacity: q == false ? 0.4 : 1, highlighted: true)
        }
        if neighbors.contains(name) {
            return Emphasis(color: Color("accentSecondary"), opacity: q == false ? 0.4 : 1, highlighted: false)
        }
        return Emphasis(color: .primary, opacity: 0.18, highlighted: false)
    }

    private func rebuild(in size: CGSize) {
        guard size.width > 1, size.height > 1 else { return }
        placed = BallCloud.place(tags: tags, in: size)
    }
}

struct PlacedTag: Identifiable {
    let tag: CloudTag
    let center: CGPoint
    let fontSize: CGFloat
    let weight: Font.Weight
    let opacity: Double

    var id: String { tag.name }
}

/// 圆盘分行排布：标签按频次降序，从中心行向上下交替填充，每行宽度受圆弦长约束。
/// 行高固定 + 行宽不超弦长 → 天然不重叠；放不下则整体缩小字号后重算。
/// ponytail: 字号按总面积缩放迭代 ≤10 次，标签量级小可接受。
enum BallCloud {
    static let hGap: CGFloat = 14
    static let vGap: CGFloat = 8
    static let padding: CGFloat = 16
    static let minFont: CGFloat = 9

    /// 确定性伪随机（SplitMix64）：以标签名做种子，保证同一标签每次布局位置稳定不乱跳。
    private struct SeededRNG {
        private var state: UInt64
        init(_ seed: UInt64) { state = seed == 0 ? 0x9E3779B97F4A7C15 : seed }
        mutating func unit() -> Double {
            state &+= 0x9E3779B97F4A7C15
            var z = state
            z = (z ^ (z >> 30)) &* 0xBF58476D1CE4E5B9
            z = (z ^ (z >> 27)) &* 0x94D049BB133111EB
            z ^= z >> 31
            return Double(z >> 11) / Double(1 << 53)
        }
    }

    /// 按「标签名 + 盐」生成稳定的 0..<1 噪声。
    private static func noise(_ name: String, _ salt: UInt64) -> Double {
        var h: UInt64 = 0xcbf29ce484222325
        for b in name.utf8 { h = (h ^ UInt64(b)) &* 0x100000001B3 }
        var rng = SeededRNG(h &+ salt &* 0x9E3779B97F4A7C15)
        return rng.unit()
    }

    static func place(tags: [CloudTag], in size: CGSize) -> [PlacedTag] {
        guard !tags.isEmpty else { return [] }
        let maxCount = tags.map(\.count).max() ?? 1
        let minCount = tags.map(\.count).min() ?? 1
        let cx = size.width / 2
        let cy = size.height / 2
        let maxRadius = max(40, min(size.width, size.height) / 2 - padding)

        func fits(_ radius: CGFloat, _ scale: CGFloat) -> Bool {
            !pack(tags, cx: cx, cy: cy, radius: radius, scale: scale, maxCount: maxCount, minCount: minCount).overflow
        }

        // 1) 缩字号，保证在面板最大半径内能全部装下
        var scale: CGFloat = 1
        for _ in 0..<10 {
            if fits(maxRadius, scale) { break }
            scale *= 0.88
        }

        // 2) 二分找恰好容纳全部标签的最小半径 → 完整的圆，随后随标签多少自动缩放，不再是面板中间的一条
        var lo: CGFloat = 1
        var hi: CGFloat = maxRadius
        if fits(hi, scale) {
            for _ in 0..<12 {
                let mid = (lo + hi) / 2
                if fits(mid, scale) { hi = mid } else { lo = mid }
            }
        }
        return pack(tags, cx: cx, cy: cy, radius: hi, scale: scale, maxCount: maxCount, minCount: minCount).placed
    }

    private struct PackResult {
        var placed: [PlacedTag]
        var overflow: Bool
    }

    private static func pack(
        _ tags: [CloudTag], cx: CGFloat, cy: CGFloat, radius: CGFloat,
        scale: CGFloat, maxCount: Int, minCount: Int
    ) -> PackResult {
        let ratio: (Int) -> Double = { count in
            maxCount > minCount ? Double(count - minCount) / Double(maxCount - minCount) : 1
        }
        let font: (Int) -> CGFloat = { max(minFont, (13 + CGFloat(ratio($0).squareRoot()) * 17) * scale) }
        let weightOf: (Int) -> Font.Weight = { ratio($0) > 0.5 ? .semibold : .regular }

        var maxHeight: CGFloat = 0
        for tag in tags {
            maxHeight = max(maxHeight, measure(tag.name, font: font(tag.count), semibold: weightOf(tag.count) == .semibold).height)
        }
        let rowHeight = maxHeight + vGap

        var placed: [PlacedTag] = []
        var idx = 0
        var overflow = false
        var rowK = 0

        while idx < tags.count {
            let offsetIndex: Int
            if rowK == 0 { offsetIndex = 0 }
            else if rowK % 2 == 1 { offsetIndex = (rowK + 1) / 2 }
            else { offsetIndex = -(rowK / 2) }
            let y = CGFloat(offsetIndex) * rowHeight
            let chord = radius * radius - y * y
            if chord <= 0 { overflow = true; break }
            let maxRowWidth = 2 * chord.squareRoot()

            var row: [(tag: CloudTag, size: CGSize, font: CGFloat, weight: Font.Weight, gap: CGFloat)] = []
            var rowWidth: CGFloat = 0
            while idx < tags.count {
                let tag = tags[idx]
                let fs = font(tag.count)
                let wt = weightOf(tag.count)
                let s = measure(tag.name, font: fs, semibold: wt == .semibold)
                let gap = hGap * CGFloat(0.6 + 0.8 * noise(tag.name, 1))
                if row.isEmpty {
                    if s.width > maxRowWidth { overflow = true; break }
                } else if rowWidth + gap + s.width > maxRowWidth {
                    break
                }
                rowWidth += gap + s.width
                row.append((tag, s, fs, wt, gap))
                idx += 1
            }
            if overflow { break }
            guard let first = row.first else { overflow = true; break }

            // 行内重排：最高频居中，其余依次向两侧展开
            var left: [(tag: CloudTag, size: CGSize, font: CGFloat, weight: Font.Weight, gap: CGFloat)] = []
            var right: [(tag: CloudTag, size: CGSize, font: CGFloat, weight: Font.Weight, gap: CGFloat)] = []
            for (k, item) in row.enumerated() where k > 0 {
                if k % 2 == 1 { right.append(item) } else { left.append(item) }
            }
            let display = Array(left.reversed()) + [first] + right

            // 行整体左右错位 + 词在行带内上下漂移 → 凌乱感（仍不重叠、高频不跑偏）
            // 中心行不动、错位量加上限，避免高频词被推出中心
            let slack = max(0, (maxRowWidth - rowWidth) / 2)
            let rowOffset = rowK == 0 ? 0 : min(slack, radius * 0.1) * CGFloat(2 * noise(first.tag.name, 3 + UInt64(rowK)) - 1)
            let leftPad = display[0].gap
            var x = cx + rowOffset - (rowWidth - leftPad) / 2
            for (j, item) in display.enumerated() {
                if j > 0 { x += item.gap }
                let jitter = (rowHeight - item.size.height) / 2 * 0.7
                    * CGFloat(2 * noise(item.tag.name, 2) - 1)
                let halfW = item.size.width / 2
                let halfH = item.size.height / 2
                let px = min(max(x + halfW, halfW), 2 * cx - halfW)
                let py = min(max(cy + y + jitter, halfH), 2 * cy - halfH)
                placed.append(PlacedTag(
                    tag: item.tag,
                    center: CGPoint(x: px, y: py),
                    fontSize: item.font,
                    weight: item.weight,
                    opacity: 0.5 + ratio(item.tag.count) * 0.5
                ))
                x += item.size.width
            }
            rowK += 1
        }
        return PackResult(placed: placed, overflow: overflow)
    }

    private static func measure(_ text: String, font: CGFloat, semibold: Bool) -> CGSize {
        let nsFont = NSFont.systemFont(ofSize: font, weight: semibold ? .semibold : .regular)
        let size = (text as NSString).size(withAttributes: [.font: nsFont])
        return CGSize(width: size.width.rounded(.up), height: size.height.rounded(.up))
    }
}
#endif
