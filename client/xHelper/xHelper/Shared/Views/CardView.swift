import SwiftUI

struct CardView: View {
    @Environment(\.colorScheme) private var colorScheme
    let title: String
    let subtitle: String?
    let headerBadge: TagBadge?
    let tags: [TagBadge]
    let footerText: String?
    let style: CardStyle
    let titleColorOverride: Color?
    let actions: [CardAction]
    let onTap: (() -> Void)?

    init(
        title: String,
        subtitle: String? = nil,
        headerBadge: TagBadge? = nil,
        tags: [TagBadge] = [],
        footerText: String? = nil,
        style: CardStyle,
        titleColorOverride: Color? = nil,
        actions: [CardAction] = [],
        onTap: (() -> Void)? = nil
    ) {
        self.title = title
        self.subtitle = subtitle
        self.headerBadge = headerBadge
        self.tags = tags
        self.footerText = footerText
        self.style = style
        self.titleColorOverride = titleColorOverride
        self.actions = actions
        self.onTap = onTap
    }

    init(config: CardConfig) {
        self.title = config.title
        self.subtitle = config.subtitle
        self.headerBadge = config.headerBadge
        self.tags = config.tags
        self.footerText = config.footerText
        self.style = config.style
        self.titleColorOverride = config.titleColorOverride
        self.actions = config.actions
        self.onTap = config.onTap
    }

    /// 浅色下卡片表面：**顶亮底沉的白霜微渐变**（单色平铺会盖死底图变化、显呆板），只 macOS 生效；
    /// 深色 / iOS 维持原样式。绿/薄荷色相留给卡片内容（标签、动作）。
    private var cardFill: AnyShapeStyle {
        #if os(macOS)
        if colorScheme == .dark { return AnyShapeStyle(style.backgroundColor) }
        return AnyShapeStyle(LinearGradient(
            colors: [Color.white.opacity(0.50), Color.white.opacity(0.26)],
            startPoint: .top, endPoint: .bottom))
        #else
        return AnyShapeStyle(style.backgroundColor)
        #endif
    }

    private var cardStroke: Color {
        #if os(macOS)
        if colorScheme == .dark { return style.accentColor.opacity(0.15) }
        return Color("assistantText").opacity(0.08)
        #else
        return style.accentColor.opacity(0.15)
        #endif
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let badge = headerBadge {
                HStack {
                    tagPill(badge)
                    Spacer()
                }
            }

            Text(title)
                .font(.system(size: 17 * style.titleScale, weight: style.titleWeight))
                .foregroundColor(titleColorOverride ?? style.titleColor)
                .lineLimit(2)
                .frame(maxWidth: .infinity, alignment: .leading)

            if let subtitle = subtitle {
                Text(subtitle)
                    .font(.system(size: 17 * style.subtitleScale, weight: style.subtitleWeight))
                    .foregroundColor(style.subtitleColor)
                    .lineLimit(style.subtitleLineLimit)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }

            // 页脚一行：标签在左、日期与按钮在右，整行随 Spacer 贴卡片底。
            if !tags.isEmpty || footerText != nil || !actions.isEmpty {
                Spacer(minLength: 6)
                HStack(spacing: 8) {
                    if !tags.isEmpty {
                        #if os(macOS)
                        TagScrollRow(spacing: 4) {
                            ForEach(tags) { tag in
                                tagPill(tag)
                            }
                        }
                        #else
                        HStack(spacing: 4) {
                            ForEach(tags) { tag in
                                tagPill(tag)
                            }
                        }
                        #endif
                    }

                    Spacer(minLength: 8)

                    if let footerText {
                        Text(footerText)
                            .font(.caption)
                            .foregroundColor(style.subtitleColor)
                            .lineLimit(1)
                            .fixedSize()
                    }

                    ForEach(actions) { action in
                        let base = action.role == .destructive ? Color.red : style.accentColor
                        Button {
                            action.action()
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: action.icon)
                                    .font(.caption)
                                Text(action.label)
                                    .font(.caption)
                            }
                            .foregroundColor(base)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(base.opacity(action.isActive ? 0.22 : 0.1))
                            .clipShape(RoundedRectangle(cornerRadius: 6))
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
        .padding(style.padding)
        .frame(minHeight: style.minHeight)
        // 撑满网格行高：LazyVGrid 只给 minHeight 的卡片不会接受更高的行高建议 → 同行卡片参差；
        // maxHeight:.infinity 接受行高 + top 对齐让内容仍顶对齐（非网格场景无多余高度，退化为按内容）。
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .background(cardFill, in: RoundedRectangle(cornerRadius: style.cornerRadius))
        .overlay(
            RoundedRectangle(cornerRadius: style.cornerRadius)
                .stroke(cardStroke, lineWidth: 1)
        )
        .contentShape(RoundedRectangle(cornerRadius: style.cornerRadius))
        .onTapGesture { onTap?() }
    }

    private func tagPill(_ badge: TagBadge) -> some View {
        TagChip(text: badge.text, color: badge.color, font: .caption2, hPad: 6, vPad: 2)
    }
}
