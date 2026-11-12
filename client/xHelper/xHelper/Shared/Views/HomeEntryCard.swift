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

    /// 单行档的细节行：标题在左（单行 tail 截断），时间/金额在右（不参与压缩）。
    struct DetailLine: Identifiable {
        let id: String
        let title: String
        let detail: String?
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
    let height: CGFloat
    /// 单行档的细节行（至多 2 行）
    let details: [DetailLine]
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
        height: CGFloat,
        details: [DetailLine] = [],
        infoRows: [InfoRow] = [],
        action: @escaping () -> Void
    ) {
        self.asset = asset
        self.title = title
        self.primary = primary
        self.secondary = secondary
        self.tint = tint
        self.layout = layout
        self.height = height
        self.details = details
        self.infoRows = infoRows
        self.action = action
    }

    var body: some View {
        Group {
            switch layout {
            case .iconOnly: iconTile
            case .info:     infoCard
            case .singleLine, .stacked: textCard
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
            .frame(width: height, height: height)
            .modifier(HomeCardChrome(hovered: hovered))
        }
        .buttonStyle(.plain)
        .help(title)
    }

    private var infoCard: some View {
        HStack(spacing: 0) {
            band(width: 116)

            if !infoRows.isEmpty {
                VStack(spacing: 6) {
                    ForEach(infoRows.prefix(3)) { row in
                        HomeInfoRowView(row: row)
                    }
                }
                .frame(width: 244)
                .padding(.horizontal, 10)
            }

            // 信息行各自是按钮，整卡不能再套一层按钮（会丢点击）——这里只有文字区可点。
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

    private var textCard: some View {
        Button(action: action) {
            HStack(spacing: 0) {
                band(width: height)
                Group {
                    if layout == .singleLine { singleLineStack } else { textStack }
                }
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

    private var textStack: some View {
        VStack(alignment: .leading, spacing: 3) {
            titleText
            primaryText
            secondaryText
        }
    }

    /// 单行档：`标题 · 主数字 ⟶ 副文案`，下面至多两行细节（104 高只放一行）。
    private var singleLineStack: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 10) {
                titleText.layoutPriority(2)
                primaryText.layoutPriority(1)
                Spacer(minLength: 12)
                secondaryText
            }
            ForEach(Array(details.prefix(height >= 120 ? 2 : 1))) { line in
                HStack(spacing: 8) {
                    Text(line.title)
                        .font(.system(size: 11))
                        .foregroundColor(.secondary.opacity(0.85))
                        .lineLimit(1)
                        .truncationMode(.tail)
                    Spacer(minLength: 6)
                    if let detail = line.detail, !detail.isEmpty {
                        Text(detail)
                            .font(.system(size: 10.5))
                            .foregroundColor(.secondary)
                            .lineLimit(1)
                            .layoutPriority(1)
                    }
                }
            }
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

/// 信息行：18pt 图标 chip + 标题 + 右侧弱化细节；hover 淡绿底 + 绿描边。
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
                    .truncationMode(.middle)
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
            .background(
                RoundedRectangle(cornerRadius: 9)
                    .fill(hovered ? accent.opacity(dark ? 0.18 : 0.12)
                                   : Color.white.opacity(dark ? 0.06 : 0.40))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 9)
                    .strokeBorder(hovered ? accent.opacity(0.45) : Color.white.opacity(dark ? 0.08 : 0.55),
                                  lineWidth: 0.5)
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
