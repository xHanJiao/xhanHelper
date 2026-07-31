import SwiftUI

struct BillDetailOverlay: View {
    let bill: Bill
    let onClose: () -> Void

    var body: some View {
        ZStack {
            Color.black.opacity(0.2)
                .ignoresSafeArea()
                .onTapGesture { onClose() }

            DetailSheet(onClose: onClose) {
                VStack(alignment: .leading, spacing: 12) {
                    Text(bill.item)
                        .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))

                    Text(String(format: "¥%.2f", bill.amount))
                        .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .bold).monospacedDigit())
                        .foregroundColor(bill.consumeType == "income" ? .green : .red)

                    Divider()

                    detailRow("日期", bill.date)
                    detailRow("一级分类", bill.category1)
                    detailRow("二级分类", bill.category2)

                    if let consumeType = bill.consumeType, !consumeType.isEmpty {
                        detailRow("消费类型", consumeType)
                    }
                    if let payType = bill.payType, !payType.isEmpty {
                        detailRow("支付方式", payType)
                    }
                    if let owner = bill.owner, !owner.isEmpty {
                        detailRow("归属人", owner)
                    }
                    if let remark = bill.remark, !remark.isEmpty {
                        detailRow("备注", remark)
                    }
                }
            }
            .frame(minWidth: 350, maxWidth: 500)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .padding(40)
            .transition(.opacity.combined(with: .scale(0.95)))
        }
        .animation(.easeOut(duration: 0.2), value: bill.id)
    }

    private func detailRow(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label)
                .font(.system(size: FontSizeManager.shared.scaledSize * 0.88))
                .foregroundColor(.secondary)
                .frame(width: 70, alignment: .leading)
            Text(value)
                .font(.system(size: FontSizeManager.shared.scaledSize * 1.0))
        }
    }
}
