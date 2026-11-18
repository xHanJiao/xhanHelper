import SwiftUI

/// 右栏首页的入口卡。
///
/// 四档按卡宽降级（见 docs/design/right-sidebar-home/design-cards-v2.md §3）：
/// 信息档 = 插画带 116 ｜ 3 条可点行 ｜ 文字区 + 箭头；单行档 = 单行 + 至多 2 行细节；
/// 三行档 = 标题 / 主数字 / 副文案；图标档 = 只有插画方块（无文字，hover 出名字）。
/// 独立成文件（不从 `BrowsePanelView` 里 private 内嵌）：单独可读、可复用到别的入口。
struct HomeEntryCard: View {

    /// 信息行左端图标的来源（都落在已有字段上，见设计文档 §4）。
    enum InfoIcon {
        case symbol(String)
        /// 需要提示的状态（过期提醒），橙色
        case warning(String)
        /// 标题首字色块（无类型字段的域靠首字区分行）
        case monogram(String)
        /// 真 favicon；取不到退回首字色块
        case favicon(host: String)
    }

    struct InfoRow: Identifiable {
        let id: String
        let title: String
        let detail: String?
        let icon: InfoIcon
        let action: () -> Void
    }

    let asset: String
    let title: String
    /// 会变的那一行（本月 ¥1,284 / 128 条 / 2 个标签 …）
    let primary: String
    /// 更弱的补充行（32 笔 / 最近 昨天），没有就不渲染
    let secondary: String?
    /// 插画区底色（token 色，低透明度）
    let tint: Color
    let layout: HomeCardLayout
    /// 卡宽：窄卡的插画带要跟着缩（`homeBandWidth`），不然文字区没地方
    let width: CGFloat
    let height: CGFloat
    /// 信息档的三条可点行（空 = 该档不渲染信息区，文字区占满）
    let infoRows: [InfoRow]
    /// 非信息档：整卡点击；信息档：文字区点击
    let action: () -> Void

    @Environment(\.colorScheme) private var colorScheme
    @State private var hovered = false

    init(
        asset: String,
        title: String,
        primary: String,
        secondary: String?,
        tint: Color,
        layout: HomeCardLayout,
        width: CGFloat,
        height: CGFloat,
        infoRows: [InfoRow] = [],
        action: @escaping () -> Void
    ) {
        self.asset = asset
        self.title = title
        self.primary = primary
        self.secondary = secondary
        self.tint = tint
        self.layout = layout
        self.width = width
        self.height = height
        self.infoRows = infoRows
        self.action = action
    }

    var body: some View {
        Group {
            switch layout {
            case .iconOnly:         iconTile
            case .info:             infoCard
            case .compactInfo:      compactCard
            case .stacked:          textCard
            }
        }
        .onHover { hovered = $0 }
        .animation(.easeOut(duration: 0.12), value: hovered)
    }

    // MARK: - 三种卡体

    private var iconTile: some View {
        Button(action: action) {
            ZStack {
                bandFill
                Image(asset)
                    .resizable()
                    .scaledToFit()
                    .padding(10)
            }
            // 填满列宽、高度取档位值：图标档在窄窗口是「宽卡 + 居中插画」，不再是方块
            .frame(maxWidth: .infinity)
            .frame(height: height)
            .modifier(HomeCardChrome(hovered: hovered))
        }
        .buttonStyle(.plain)
        .help(title)
    }

    private var infoCard: some View {
        HStack(spacing: 0) {
            Button(action: action) {
                band(width: 116)
            }
            .buttonStyle(.plain)
            .help(title)
            .accessibilityLabel(title)

            if !infoRows.isEmpty {
                VStack(spacing: 6) {
                    ForEach(infoRows.prefix(3)) { row in
                        HomeInfoRowView(row: row)
                    }
                }
                // 行区宽度随卡宽走（560 → 244，≥636 → 320）：宽卡别让行区白占着、文字区空着
                .frame(width: SidebarHomeFormat.homeInfoRowsWidth(cardWidth: width))
                .padding(.horizontal, 10)
                // 信息行以外全是可点区：行间 6pt 缝、左右 10pt 与上下空余都进面板。
                // 垫底按钮在行按钮后面——点行仍走行自己的动作，空处才落到这层。
                .frame(maxHeight: .infinity)
                .background {
                    // 只补鼠标热区；卡片动作由文字区按钮承载，别让 VoiceOver/全键盘多一个无名焦点
                    Button(action: action) { Color.clear.contentShape(Rectangle()) }
                        .buttonStyle(.plain)
                        .accessibilityHidden(true)
                }
            }

            // 信息行各自是按钮，整卡不能再套一层按钮（会丢点击）——所以文字区自带按钮。
            Button(action: action) {
                HStack(spacing: 0) {
                    textStack
                        .padding(.horizontal, 14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    chevron
                }
                // 撑满卡高，否则 164 高的卡里只有中间 ~50pt 能点
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(title)
        }
        .frame(height: height)
        .modifier(HomeCardChrome(hovered: hovered))
    }

    /// 紧凑信息档（卡宽 340–560）：头部一行 `标题 · 主数字`，下面 3 条可点行（与信息档同行数）。
    /// 跟信息档的差别只是**行不单占一列**——卡窄到三列并排时文字区只剩 ~100pt，
    /// 把标题压到行上方，行就能吃满剩余宽度（比信息档的 244 还宽）。
    private var compactCard: some View {
        HStack(spacing: 0) {
            // 插画带跟信息档一样封顶 116：比 0.42 的默认口径窄，省下的宽度全给行
            // 这一档此前（单行/三行）是整卡按钮，插画带不做成可点会被当成"卡片坏了"
            Button(action: action) {
                band(width: min(116, SidebarHomeFormat.homeBandWidth(cardWidth: width, cardHeight: height)))
            }
            .buttonStyle(.plain)
            .help(title)
            .accessibilityLabel(title)

            VStack(alignment: .leading, spacing: 6) {
                Button(action: action) {
                    HStack(spacing: 10) {
                        titleText.layoutPriority(2)
                        primaryText.layoutPriority(1)
                        Spacer(minLength: 6)
                        if showsSecondary { secondaryText }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(title)

                ForEach(infoRows.prefix(3)) { row in
                    HomeInfoRowView(row: row)
                }

                // 只有 1 条行的卡（如浏览器只开着一个标签）不居中：头部和第一条行跟邻居
                // 对齐，空位留在卡底，否则同一行卡片的信息区会错位（用户 2026-11-13）。
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 14)
            // 164 里放「头部 ~17 + 3×28 + 4 个 6pt 间距」= 125，加上下各 14 = 153，余 11 留给卡底 Spacer
            .padding(.vertical, 14)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            // 头部行 / 信息行以外（内距、行间缝、卡底空位）也都能进面板：
            // 垫底按钮在按钮后面，碰不到行的点击。
            .background {
                // 同信息档：只补鼠标热区，读屏焦点留给头部行按钮
                Button(action: action) { Color.clear.contentShape(Rectangle()) }
                    .buttonStyle(.plain)
                    .accessibilityHidden(true)
            }

            Button(action: action) {
                chevron
                    .frame(maxHeight: .infinity)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
        }
        .frame(height: height)
        .modifier(HomeCardChrome(hovered: hovered))
    }

    private var textCard: some View {
        Button(action: action) {
            HStack(spacing: 0) {
                band(width: SidebarHomeFormat.homeBandWidth(cardWidth: width, cardHeight: height))
                textStack
                    .padding(.horizontal, 14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                chevron
            }
            .frame(height: height)
            .modifier(HomeCardChrome(hovered: hovered))
        }
        .buttonStyle(.plain)
        .help(title)
    }

    // MARK: - 组成件

    private var bandFill: some View {
        LinearGradient(
            colors: [
                tint.opacity(colorScheme == .dark ? 0.20 : 0.14),
                tint.opacity(colorScheme == .dark ? 0.06 : 0.04),
            ],
            startPoint: .topLeading,
            endPoint: .bottomTrailing
        )
    }

    private func band(width: CGFloat) -> some View {
        ZStack {
            bandFill
            Image(asset)
                .resizable()
                .scaledToFit()
                .padding(6)
        }
        .frame(width: width, height: height)
        .overlay(alignment: .trailing) {
            Rectangle()
                .fill(Color.primary.opacity(0.06))
                .frame(width: 0.5)
        }
    }

    private var titleText: some View {
        Text(title)
            .font(.system(size: 13.5, weight: .semibold))
            .foregroundColor(.primary)
            .lineLimit(1)
    }

    private var primaryText: some View {
        Text(primary)
            .font(.system(size: 12.5, weight: .medium))
            .foregroundColor(Color("appAccent"))
            .monospacedDigit()
            .lineLimit(1)
    }

    @ViewBuilder private var secondaryText: some View {
        if let secondary, !secondary.isEmpty {
            Text(secondary)
                .font(.system(size: 11))
                .foregroundColor(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
        }
    }

    /// 信息行已经给出同样的时间或补充信息时，卡头第三行就是重复说两遍（还会并排
    /// 出现两套时间口径：卡头「下一条 11月15日」对行内「23 天前」）。
    ///
    /// 只有信息档 / 紧凑信息档会把行画出来，所以只在这两档且真有行时藏；三行档的第三行本来
    /// 就是副文案，照旧按卡宽决定。
    private var showsSecondary: Bool {
        switch layout {
        case .info, .compactInfo:
            return infoRows.isEmpty
        case .stacked:
            // 窄卡只留「标题 / 主数字」两行：第三行在 200 出头的卡里会被截成「上次停在 Dee…」
            return width >= SidebarHomeFormat.homeSecondaryMinCardWidth
        case .iconOnly:
            return false
        }
    }

    private var textStack: some View {
        VStack(alignment: .leading, spacing: 3) {
            titleText
            primaryText
            if showsSecondary { secondaryText }
        }
    }

    private var chevron: some View {
        Image(systemName: "chevron.right")
            .font(.system(size: 9, weight: .semibold))
            .foregroundColor(.secondary.opacity(0.6))
            .padding(.trailing, 14)
    }
}

/// 卡片外壳：底 / 描边 / 圆角 / 悬停提亮与上浮。三种卡体共用一份。
private struct HomeCardChrome: ViewModifier {
    let hovered: Bool
    @Environment(\.colorScheme) private var colorScheme

    private var fill: Color {
        colorScheme == .dark
            ? Color.white.opacity(hovered ? 0.13 : 0.07)
            : Color.white.opacity(hovered ? 0.66 : 0.44)
    }

    private var stroke: Color {
        colorScheme == .dark ? Color.white.opacity(0.1) : Color.white.opacity(0.66)
    }

    func body(content: Content) -> some View {
        content
            .background(RoundedRectangle(cornerRadius: 14).fill(fill))
            .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(stroke, lineWidth: 0.5))
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .shadow(color: .black.opacity(hovered ? 0.1 : 0), radius: 6, y: 3)
            .offset(y: hovered ? -1.5 : 0)
            .contentShape(RoundedRectangle(cornerRadius: 14))
    }
}

/// 信息行：18pt 图标 chip + 标题 + 右侧弱化细节。常态无底无描边，hover 才浮出淡绿底 + 绿描边。
private struct HomeInfoRowView: View {
    let row: HomeEntryCard.InfoRow

    /// 信息行统一用主题绿（方案 §4），不跟卡片的插画色调走——账单是褐底卡，绿行反而更清楚。
    private let accent = Color("appAccent")

    @ObservedObject private var faviconStore = FaviconStore.shared
    @Environment(\.colorScheme) private var colorScheme
    @State private var hovered = false

    private var dark: Bool { colorScheme == .dark }

    var body: some View {
        Button(action: row.action) {
            HStack(spacing: 8) {
                icon
                Text(row.title)
                    .font(.system(size: 11.5, weight: .medium))
                    .foregroundColor(.primary.opacity(0.88))
                    .lineLimit(1)
                    // 留头（用户 2026-11-13）：标题多是「对象 + 补充」，头几个字才区分得开；
                    // 截中间会同时丢头和尾，读起来像乱码。
                    .truncationMode(.tail)
                Spacer(minLength: 6)
                if let detail = row.detail, !detail.isEmpty {
                    Text(detail)
                        .font(.system(size: 10))
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
            .padding(.horizontal, 8)
            .frame(height: 28)
            // 常态不铺底也不描边：卡片本身已经浮起一层，行再套一层白卡会重得压过标题。
            // 可点感交给 hover —— 鼠标进来才浮出淡绿底 + 绿描边。
            .background(
                RoundedRectangle(cornerRadius: 9)
                    .fill(hovered ? accent.opacity(dark ? 0.18 : 0.12) : Color.clear)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 9)
                    .strokeBorder(hovered ? accent.opacity(0.45) : Color.clear, lineWidth: 0.5)
            )
            .contentShape(RoundedRectangle(cornerRadius: 9))
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 }
        .help(row.title)
    }

    @ViewBuilder private var icon: some View {
        switch row.icon {
        case .symbol(let name):
            chip {
                Image(systemName: name)
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundColor(accent)
            }
        case .warning(let name):
            chip(warning: true) {
                Image(systemName: name)
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundColor(.orange)
            }
        case .monogram(let text):
            chip {
                Text(text)
                    .font(.system(size: 9, weight: .bold))
                    .foregroundColor(accent)
            }
        case .favicon(let host):
            if let image = faviconStore.image(forHost: host) {
                faviconImage(image)
            } else {
                chip {
                    Text(SidebarHomeFormat.monogram(host))
                        .font(.system(size: 9, weight: .bold))
                        .foregroundColor(accent)
                }
            }
        }
    }

    private func chip<Content: View>(warning: Bool = false, @ViewBuilder content: () -> Content) -> some View {
        content()
            .frame(width: 18, height: 18)
            .background(
                RoundedRectangle(cornerRadius: 5)
                    .fill((warning ? Color.orange : accent).opacity(dark ? 0.24 : 0.14))
            )
    }

    private func faviconImage(_ image: FaviconImage) -> some View {
        Group {
            #if os(macOS)
            Image(nsImage: image).resizable().interpolation(.medium)
            #else
            Image(uiImage: image).resizable().interpolation(.medium)
            #endif
        }
        .frame(width: 16, height: 16)
    }
}
