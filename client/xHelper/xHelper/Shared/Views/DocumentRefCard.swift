import SwiftUI

/// 聊天里的文档入口卡片：整卡可点，点了把右栏切到文档面板并定位。
/// 只展示标题 + 摘要（其余元素按观感迭代裁掉）。
///
/// `width` / `height` 只在**横向卡片条**里给（多张卡要等宽等高、条子按固定卡宽算排布）；
/// 都不传＝按内容撑开，就是单卡的样子。
struct DocumentRefCard: View {
    let ref: DocumentRef
    var width: CGFloat? = nil
    var height: CGFloat? = nil
    let onOpen: () -> Void

    var body: some View {
        Button(action: onOpen) {
            VStack(alignment: .leading, spacing: 6) {
                Text(ref.title)
                    .font(.system(size: 13.5, weight: .semibold))
                    .foregroundColor(Color("assistantText"))
                    .lineLimit(1)
                    .truncationMode(.middle)

                if let preview = ref.preview, !preview.isEmpty {
                    Text(preview)
                        .font(.system(size: 11.5))
                        .foregroundColor(Color("assistantText").opacity(0.6))
                        .lineLimit(3)
                        .truncationMode(.tail)
                }
            }
            // 单卡：内容最宽 = 条子里的卡宽（同一个 `ArtifactCardMetrics.width`，别再写一个字面量）；
            // 条子里：吃满条子给的固定卡宽
            .frame(maxWidth: width == nil ? ArtifactCardMetrics.width : .infinity, alignment: .leading)
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            // 定高要在背景之前，白霜底才能撑满整格（多卡等高靠它）
            .frame(width: width, height: height, alignment: .top)
            .background(Color("surfaceInput"), in: RoundedRectangle(cornerRadius: 11))
        }
        .buttonStyle(.plain)
        .help("在右侧栏打开《\(ref.title)》")
    }
}

#Preview("单张") {
    DocumentRefCard(ref: DocumentRef(id: "1", title: "リオグランデ西新宿 · 入住与落地指南", preview: "**10/17 入住（15:00 后）— 10/21 退房，房间 102。** 现场照着做就行。"), onOpen: {})
        .padding(24)
        .background(Color("chatBackground"))
}
