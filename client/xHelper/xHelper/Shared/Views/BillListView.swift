import SwiftUI
#if os(iOS)
import UIKit
#endif

struct BillListView: View {
    let apiClient: APIClient
    @State private var bills: [Bill] = []
    @State private var isLoading = true
    @State private var showExport = false
    @State private var startDate = Date()
    @State private var endDate = Date()
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if let err = errorMessage {
                VStack(spacing: 12) {
                    Text(err).foregroundColor(.secondary)
                    Button("重试") { Task { await load() } }
                }
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
        .toolbar {
            Button("导出") { showExport = true }
        }
        .sheet(isPresented: $showExport) {
            NavigationStack {
                Form {
                    DatePicker("开始日期", selection: $startDate, displayedComponents: .date)
                    DatePicker("结束日期", selection: $endDate, displayedComponents: .date)
                }
                .navigationTitle("导出 CSV")
                #if os(iOS)
                .navigationBarTitleDisplayMode(.inline)
                #endif
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("取消") { showExport = false }
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("导出") {
                            Task { await doExport() }
                            showExport = false
                        }
                    }
                }
            }
        }
        .task { await load() }
    }

    private func load() async {
        do {
            let resp = try await apiClient.getBills()
            bills = resp.bills
            errorMessage = nil
        } catch {
            LogBuffer.shared.error("BillListView.load: \(error)")
            errorMessage = "加载失败: \(error.localizedDescription)"
        }
        isLoading = false
    }

    private func doExport() async {
        let df = DateFormatter()
        df.dateFormat = "yyyy-MM-dd"
        do {
            let csv = try await apiClient.exportBillsCSV(
                startDate: df.string(from: startDate),
                endDate: df.string(from: endDate)
            )
            let url = FileManager.default.temporaryDirectory.appendingPathComponent("bills.csv")
            try csv.write(to: url, atomically: true, encoding: .utf8)
            #if os(iOS)
            let av = UIActivityViewController(activityItems: [url], applicationActivities: nil)
            if let scene = UIApplication.shared.connectedScenes.first as? UIWindowScene,
               let vc = scene.windows.first?.rootViewController {
                vc.present(av, animated: true)
            }
            #endif
        } catch {
            LogBuffer.shared.error("BillListView.export: \(error)")
            errorMessage = "导出失败: \(error.localizedDescription)"
        }
    }
}
