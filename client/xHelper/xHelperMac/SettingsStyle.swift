import SwiftUI

/// 设置页子页面统一的玻璃卡片容器：accent 图标 + 加粗标题 + 灰色作用域说明 + 内容行。
/// 规范见 docs/design/mac-apps/settings-sheet-style.md
struct SettingsCard<Content: View>: View {
    let icon: String
    let title: String
    let hint: String?
    private let content: () -> Content

    init(icon: String, title: String, hint: String? = nil, @ViewBuilder content: @escaping () -> Content) {
        self.icon = icon
        self.title = title
        self.hint = hint
        self.content = content
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: icon)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Color("appAccent"))
                    .frame(width: 18)
                Text(title)
                    .font(.system(size: 13, weight: .semibold))
                if let hint {
                    Text(hint)
                        .font(.system(size: 11))
                        .foregroundStyle(.tertiary)
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)

            Divider().opacity(0.6)

            VStack(alignment: .leading, spacing: 0) {
                content()
            }
            .padding(.vertical, 4)
        }
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .strokeBorder(.primary.opacity(0.08), lineWidth: 0.5)
        )
    }
}

/// 键盘键帽（快捷键展示等场景统一使用）。
struct KeycapView: View {
    let label: String

    var body: some View {
        Text(label)
            .font(.system(size: 12, weight: .medium, design: .monospaced))
            .frame(minWidth: 24, minHeight: 22)
            .padding(.horizontal, 6)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .strokeBorder(.primary.opacity(0.1), lineWidth: 0.5)
            )
    }
}
