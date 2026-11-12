import SwiftUI

/// 右栏首页的入口卡：左侧正方形插画 + 右侧标题/主数字/副文案 + 右箭头。
///
/// 尺寸与配色见 docs/design/right-sidebar-home/design.md §4：
/// 底 `white 44%`（深色 7%），悬停提亮并轻微上浮；插画区宽度 = 卡片高度。
/// 独立成文件（不从 `BrowsePanelView` 里 private 内嵌）：把入口卡从 1000+ 行的面板视图里拆出来，
/// 单独可读、可复用到别的入口。
struct HomeEntryCard: View {
    let asset: String
    let title: String
    /// 会变的那一行（本月 ¥1,284 / 128 条 …）
    let primary: String
    /// 更弱的补充行（32 笔 / 最近 昨天），没有就不渲染
    let secondary: String?
    /// 插画区底色（token 色，低透明度）
    let tint: Color
    let height: CGFloat
    let action: () -> Void

    @Environment(\.colorScheme) private var colorScheme
    @State private var hovered = false

    init(
        asset: String,
        title: String,
        primary: String,
        secondary: String?,
        tint: Color,
        height: CGFloat,
        action: @escaping () -> Void
    ) {
        self.asset = asset
        self.title = title
        self.primary = primary
        self.secondary = secondary
        self.tint = tint
        self.height = height
        self.action = action
    }

    private var fill: Color {
        colorScheme == .dark
            ? Color.white.opacity(hovered ? 0.13 : 0.07)
            : Color.white.opacity(hovered ? 0.66 : 0.44)
    }

    private var stroke: Color {
        colorScheme == .dark ? Color.white.opacity(0.1) : Color.white.opacity(0.66)
    }

    var body: some View {
        Button(action: action) {
            HStack(spacing: 0) {
                ZStack {
                    // 12~16% 的渐变底（左上浓、右下淡），让插画区像一张"图"而不是一块色块
                    LinearGradient(
                        colors: [
                            tint.opacity(colorScheme == .dark ? 0.20 : 0.14),
                            tint.opacity(colorScheme == .dark ? 0.06 : 0.04),
                        ],
                        startPoint: .topLeading,
                        endPoint: .bottomTrailing
                    )
                    Image(asset)
                        .resizable()
                        .scaledToFit()
                        .padding(6)
                }
                .frame(width: height, height: height)
                .overlay(alignment: .trailing) {
                    Rectangle()
                        .fill(Color.primary.opacity(0.06))
                        .frame(width: 0.5)
                }

                VStack(alignment: .leading, spacing: 3) {
                    Text(title)
                        .font(.system(size: 13.5, weight: .semibold))
                        .foregroundColor(.primary)
                        .lineLimit(1)
                    Text(primary)
                        .font(.system(size: 12.5, weight: .medium))
                        .foregroundColor(Color("appAccent"))
                        .monospacedDigit()
                        .lineLimit(1)
                    if let secondary, !secondary.isEmpty {
                        Text(secondary)
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }
                }
                .padding(.horizontal, 14)
                .frame(maxWidth: .infinity, alignment: .leading)

                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundColor(.secondary.opacity(0.6))
                    .padding(.trailing, 14)
            }
            .frame(height: height)
            .background(RoundedRectangle(cornerRadius: 14).fill(fill))
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .strokeBorder(stroke, lineWidth: 0.5)
            )
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .shadow(
                color: .black.opacity(hovered ? 0.1 : 0),
                radius: 6, y: 3
            )
            .offset(y: hovered ? -1.5 : 0)
            .contentShape(RoundedRectangle(cornerRadius: 14))
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 }
        .animation(.easeOut(duration: 0.12), value: hovered)
        .help(title)
    }
}
