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
    let menuActions: [CardAction]
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
        menuActions: [CardAction] = [],
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
        self.menuActions = menuActions
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
        self.menuActions = config.menuActions
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
                // 右上角「⋯」的避让位：两行标题不许钻到菜单按钮底下
                .padding(.trailing, menuActions.isEmpty ? 0 : 22)
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

                    ForEach(actions.indices, id: \.self) { index in
                        let action = actions[index]
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
                        .help(action.help ?? (action.label.isEmpty ? "操作" : action.label))
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
        .overlay(alignment: .topTrailing) {
            if !menuActions.isEmpty {
                CardMoreMenu(actions: menuActions)
                    // 离圆角留一点余量：贴边会压到描边上
                    .padding(.top, 6)
                    .padding(.trailing, 6)
            }
        }
        .contentShape(RoundedRectangle(cornerRadius: style.cornerRadius))
        .onTapGesture { onTap?() }
    }

    private func tagPill(_ badge: TagBadge) -> some View {
        TagChip(text: badge.text, color: badge.color, font: .caption2, hPad: 6, vPad: 2)
    }
}

/// 卡片右上角的「⋯」：管理类动作收进一个下拉菜单（见 `docs/design/card-more-menu`）。
///
/// 常显、低对比（不做 hover 才出现）：iOS 没有 hover，隐式按钮等于不可发现。
/// 危险项前自动插分隔线、走系统红 —— 让删除和上面几项在扫视时就分开。
private struct CardMoreMenu: View {
    let actions: [CardAction]
    @State private var isHovered = false

    private var firstDestructive: Int? {
        actions.firstIndex { $0.role == .destructive }
    }

    var body: some View {
        Menu {
            ForEach(actions.indices, id: \.self) { index in
                let action = actions[index]
                if index == firstDestructive {
                    Divider()
                }
                Button(role: action.role == .destructive ? .destructive : nil) {
                    action.action()
                } label: {
                    Label(action.label, systemImage: action.icon)
                }
            }
        } label: {
            Image(systemName: "ellipsis")
                .font(.system(size: 12, weight: .semibold))
                // 0.55：非文本控件要 3:1，0.4 落在 2.4:1（数值放在这里，别再凭观感往下调）
                .foregroundColor(Color("assistantText").opacity(isHovered ? 0.75 : 0.55))
                .frame(width: 22, height: 18)
                .background(
                    Color("assistantText").opacity(isHovered ? 0.08 : 0),
                    in: RoundedRectangle(cornerRadius: 5)
                )
                .contentShape(Rectangle())
        }
        #if os(macOS)
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        #endif
        .fixedSize()
        .onHover { isHovered = $0 }
        .help("更多操作")
        .accessibilityLabel("更多操作")
    }
}
