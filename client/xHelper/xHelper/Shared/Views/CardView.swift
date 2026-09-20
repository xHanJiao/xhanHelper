import SwiftUI

struct CardView: View {
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

            if !tags.isEmpty || footerText != nil {
                #if os(macOS)
                HStack(spacing: 8) {
                    TagScrollRow(spacing: 4) {
                        ForEach(tags) { tag in
                            tagPill(tag)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    if let footerText {
                        Text(footerText)
                            .font(.caption)
                            .foregroundColor(style.subtitleColor)
                            .lineLimit(1)
                            .fixedSize()
                    }
                }
                #else
                HStack(spacing: 4) {
                    ForEach(tags) { tag in
                        tagPill(tag)
                    }
                    Spacer(minLength: 8)
                    if let footerText {
                        Text(footerText)
                            .font(.caption)
                            .foregroundColor(style.subtitleColor)
                            .lineLimit(1)
                    }
                }
                #endif
            }

            if !actions.isEmpty {
                HStack(spacing: 8) {
                    Spacer()
                    ForEach(actions) { action in
                        Button {
                            action.action()
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: action.icon)
                                    .font(.caption)
                                Text(action.label)
                                    .font(.caption)
                            }
                            .foregroundColor(action.role == .destructive ? .red : style.accentColor)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(action.role == .destructive ? Color.red.opacity(0.08) : style.accentColor.opacity(0.1))
                            .clipShape(RoundedRectangle(cornerRadius: 6))
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
        .padding(style.padding)
        .frame(minHeight: style.minHeight)
        .background(style.backgroundColor, in: RoundedRectangle(cornerRadius: style.cornerRadius))
        .overlay(
            RoundedRectangle(cornerRadius: style.cornerRadius)
                .stroke(style.accentColor.opacity(0.15), lineWidth: 1)
        )
        .contentShape(RoundedRectangle(cornerRadius: style.cornerRadius))
        .onTapGesture { onTap?() }
    }

    private func tagPill(_ badge: TagBadge) -> some View {
        TagChip(text: badge.text, color: badge.color, font: .caption2, hPad: 6, vPad: 2)
    }
}
