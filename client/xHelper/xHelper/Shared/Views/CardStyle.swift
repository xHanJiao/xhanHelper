import SwiftUI

struct TagBadge: Identifiable {
    let id = UUID()
    let text: String
    let color: Color
}

extension String {
    /// 逗号分隔的标签串 → 卡片标签胶囊（服务端已归一化，这里只切分去空格）。
    var tagBadges: [TagBadge] {
        split(separator: ",")
            .map { TagBadge(text: $0.trimmingCharacters(in: .whitespaces), color: Color("appAccent")) }
            .filter { !$0.text.isEmpty }
    }
}

extension Optional where Wrapped == String {
    /// 让 `doc.tags` / `artifact.tags` 这类可选串直接取标签胶囊（nil → 空）。
    var tagBadges: [TagBadge] { (self ?? "").tagBadges }
}

struct CardAction: Identifiable {
    /// 稳定身份：同一张卡片每次重新求值不该换 id（`UUID()` 会让 `ForEach` 当新视图重建）。
    var id: String { "\(icon)|\(label)" }
    let label: String
    let icon: String
    let role: CardActionRole
    /// 选中态（如反馈按钮已赞/已踩）：底色加重，图标由调用方传 `.fill` 版。
    let isActive: Bool
    /// tooltip / 无障碍名。留空时纯图标按钮（页脚反馈）也要有个名字。
    let help: String?
    let action: () -> Void

    init(
        label: String,
        icon: String,
        role: CardActionRole,
        isActive: Bool = false,
        help: String? = nil,
        action: @escaping () -> Void
    ) {
        self.label = label
        self.icon = icon
        self.role = role
        self.isActive = isActive
        self.help = help
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
    /// 右上角「⋯」菜单里的管理类动作（分享/下载/导出/删除）。
    /// 与 `actions` 分工：`actions` 是页脚按钮（一级操作，如 👍/👎 要一眼见状态），
    /// `menuActions` 是收进菜单的二级操作。选择态由调用方传 `[]` 让位给勾选圈。
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
    /// 卡片最小高度（`var`：真实默认值 130 要让各样式能覆盖，`let` 带默认值不进 memberwise init）。
    var minHeight: CGFloat = 130
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
        minHeight: 150,
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

    static let event = CardStyle(
        accentColor: Color("appAccent"),
        backgroundColor: Color("appAccent").opacity(0.08),
        titleColor: .primary,
        subtitleColor: .secondary,
        cornerRadius: 12,
        padding: EdgeInsets(top: 14, leading: 14, bottom: 14, trailing: 14),
        subtitleLineLimit: 3
    )

    /// 备忘：内容通常只有一行，卡矮一档；是否过期由调用方加标签区分（不占样式位）。
    static let reminder = CardStyle(
        accentColor: Color("appAccent"),
        backgroundColor: Color("appAccent").opacity(0.08),
        titleColor: .primary,
        subtitleColor: .secondary,
        cornerRadius: 12,
        padding: EdgeInsets(top: 14, leading: 14, bottom: 14, trailing: 14),
        minHeight: 104,
        subtitleLineLimit: 3
    )
}
