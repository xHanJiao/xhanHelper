import SwiftUI

/// 聊天里的 HTML 产物入口卡片：整卡可点，点了在右栏打开。
/// 观感与 `DocumentRefCard` 一致（标题 + 摘要 ≤3 行、无外框、定宽）。
///
/// 数据来自消息的 `artifacts` 字段（服务端下发）；正文不进消息，点开时才按 id 拉
/// （见 docs/design/html-artifact §4.1）。
struct HTMLArtifactCard: View {
    let title: String
    let summary: String
    let onOpen: () -> Void

    var body: some View {
        Button(action: onOpen) {
            VStack(alignment: .leading, spacing: 6) {
                Text(title.isEmpty ? "页面" : title)
                    .font(.system(size: 13.5, weight: .semibold))
                    .foregroundColor(Color("assistantText"))
                    .lineLimit(1)
                    .truncationMode(.middle)

                if !summary.isEmpty {
                    Text(summary)
                        .font(.system(size: 11.5))
                        .foregroundColor(Color("assistantText").opacity(0.6))
                        .lineLimit(3)
                        .truncationMode(.tail)
                }
            }
            .frame(maxWidth: ArtifactCardMetrics.width, alignment: .leading)
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(Color("surfaceInput"), in: RoundedRectangle(cornerRadius: 11))
        }
        .buttonStyle(.plain)
        .help("在右侧栏打开《\(title)》")
    }
}

#Preview("单张") {
    HTMLArtifactCard(title: "三方案对比", summary: "按成本、维护性、迁移难度横向对比，结论偏 B。", onOpen: {})
        .padding(24)
        .background(Color("chatBackground"))
}
