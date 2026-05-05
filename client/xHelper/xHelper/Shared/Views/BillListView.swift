import SwiftUI

struct BillListView: View {
    let apiClient: APIClient
    @State private var bills: [Bill] = []
    @State private var isLoading = true

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if bills.isEmpty {
                Text("暂无账单")
                    .foregroundColor(.secondary)
            } else {
                List(bills) { bill in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text(bill.item)
                                .font(.body)
                            Spacer()
                            Text(String(format: "%.2f 元", bill.amount))
                                .font(.body.monospacedDigit())
                        }
                        HStack {
                            Text("\(bill.category1)/\(bill.category2)")
                                .font(.caption)
                                .foregroundColor(.secondary)
                            Spacer()
                            Text(bill.date)
                                .font(.caption2)
                                .foregroundColor(.secondary)
                        }
                    }
                    .padding(.vertical, 2)
                }
            }
        }
        .navigationTitle("账单记录")
        .task { await load() }
    }

    private func load() async {
        do {
            bills = try await apiClient.getBills()
        } catch {}
        isLoading = false
    }
}
