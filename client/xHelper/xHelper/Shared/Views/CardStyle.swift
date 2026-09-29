import SwiftUI

struct TagBadge: Identifiable {
    let id = UUID()
    let text: String
    let color: Color
}

struct CardAction: Identifiable {
    let id = UUID()
    let label: String
    let icon: String
    let role: CardActionRole
    /// 选中态（如反馈按钮已赞/已踩）：底色加重，图标由调用方传 `.fill` 版。
    let isActive: Bool
    let action: () -> Void

    init(label: String, icon: String, role: CardActionRole, isActive: Bool = false, action: @escaping () -> Void) {
        self.label = label
        self.icon = icon
        self.role = role
        self.isActive = isActive
        self.action = action
    }
}

enum CardActionRole {
    case normal
    case destructive
}

struct CardConfig {
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
}

struct CardStyle {
    let accentColor: Color
    let backgroundColor: Color
    let titleScale: CGFloat = 1.0
    let titleWeight: Font.Weight = .semibold
    let titleColor: Color
    let subtitleScale: CGFloat = 0.76
    let subtitleWeight: Font.Weight = .regular
    let subtitleColor: Color
    let cornerRadius: CGFloat
    let padding: EdgeInsets
    let minHeight: CGFloat = 130
    let subtitleLineLimit: Int?

    static let agent = CardStyle(
        accentColor: Color("appAccent"),
        backgroundColor: Color("appAccent").opacity(0.08),
        titleColor: .primary,
        subtitleColor: .secondary,
        cornerRadius: 12,
        padding: EdgeInsets(top: 14, leading: 14, bottom: 14, trailing: 14),
        subtitleLineLimit: 3
    )

    static let memory = CardStyle(
        accentColor: Color("appAccent"),
        backgroundColor: Color("appAccent").opacity(0.08),
        titleColor: .primary,
        subtitleColor: .secondary,
        cornerRadius: 12,
        padding: EdgeInsets(top: 14, leading: 14, bottom: 14, trailing: 14),
        subtitleLineLimit: 3
    )

    static let note = CardStyle(
        accentColor: Color("appAccent"),
        backgroundColor: Color("appAccent").opacity(0.08),
        titleColor: .primary,
        subtitleColor: .secondary,
        cornerRadius: 12,
        padding: EdgeInsets(top: 14, leading: 14, bottom: 14, trailing: 14),
        subtitleLineLimit: 3
    )

    static let document = CardStyle(
        accentColor: Color("appAccent"),
        backgroundColor: Color("appAccent").opacity(0.08),
        titleColor: .primary,
        subtitleColor: .secondary,
        cornerRadius: 12,
        padding: EdgeInsets(top: 14, leading: 14, bottom: 14, trailing: 14),
        subtitleLineLimit: 3
    )

    static let theme = CardStyle(
        accentColor: .mint,
        backgroundColor: Color.mint.opacity(0.08),
        titleColor: .primary,
        subtitleColor: .secondary,
        cornerRadius: 12,
        padding: EdgeInsets(top: 14, leading: 14, bottom: 14, trailing: 14),
        subtitleLineLimit: 3
    )
}
