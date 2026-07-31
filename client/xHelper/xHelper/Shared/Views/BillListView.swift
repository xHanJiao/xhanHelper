import SwiftUI
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

enum FilterType { case date, category }

struct BillListView: View {
    @StateObject var viewModel: BillViewModel

    let baseURL: String
    let token: String

    @State private var activeFilter: FilterType?
    @State private var draftStart = Date()
    @State private var draftEnd = Date()

    private var distinctCategories: [String] {
        let cats = Set(viewModel.bills.map { $0.category1 }).filter { !$0.isEmpty }
        return Array(cats).sorted()
    }

    var body: some View {
        PageContainer(
            title: "账单",
            icon: "yensign.circle",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.bills.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有账单记录",
            onRetry: { Task { await viewModel.load() } }
        ) {
            VStack(spacing: 0) {
                HStack {
                    Spacer()
                    Button {
                        exportCSV()
                    } label: {
                        Image(systemName: "square.and.arrow.up")
                            .font(.system(size: 14).weight(.medium))
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("导出 CSV")
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)

                if viewModel.bills.isEmpty && !viewModel.isLoading {
                    EmptyView()
                } else {
                    DataTable(
                        items: viewModel.bills,
                        hasMore: viewModel.hasMore,
                        onLoadMore: { await viewModel.loadMore() },
                        filterContent: activeFilter != nil ? {
                            AnyView(
                                ZStack(alignment: .topTrailing) {
                                    filterPanel
                                        .padding(.horizontal, 12)
                                        .padding(.vertical, 8)

                                    Button { activeFilter = nil } label: {
                                        Image(systemName: "xmark.circle.fill")
                                            .font(.system(size: 13))
                                            .foregroundColor(.secondary)
                                    }
                                    .buttonStyle(.plain)
                                    .padding(14)
                                }
                                .frame(maxWidth: .infinity)
                                .fixedSize(horizontal: false, vertical: true)
                            )
                        } : nil
                    ) {
                        tableColumn("日期", width: .fixed(90), headerAccessory: { _ in
                            AnyView(
                                Button {
                                    activeFilter = (activeFilter == .date) ? nil : .date
                                } label: {
                                    Image(systemName: "line.3.horizontal.decrease.circle")
                                        .font(.system(size: 10).weight(.medium))
                                        .foregroundColor(viewModel.filterStartDate.isEmpty ? .secondary : Color("appAccent"))
                                }
                                .buttonStyle(.plain)
                                .help("日期筛选")
                            )
                        }) { (b: Bill) in
                            Text(b.date).font(.caption).foregroundColor(.secondary)
                        }
                        tableColumn("项目", tooltip: { $0.item }) { (b: Bill) in
                            Text(b.item).lineLimit(1)
                        }
                        tableColumn("分类", width: .fixed(100), tooltip: { $0.category2.isEmpty ? $0.category1 : "\($0.category1)/\($0.category2)" }, headerAccessory: { _ in
                            AnyView(
                                Button {
                                    activeFilter = (activeFilter == .category) ? nil : .category
                                } label: {
                                    Image(systemName: "line.3.horizontal.decrease.circle")
                                        .font(.system(size: 10).weight(.medium))
                                        .foregroundColor(viewModel.filterCategory.isEmpty ? .secondary : Color("appAccent"))
                                }
                                .buttonStyle(.plain)
                                .help("分类筛选")
                            )
                        }) { (b: Bill) in
                            Text(b.category2.isEmpty ? b.category1 : "\(b.category1)/\(b.category2)")
                                .font(.caption).foregroundColor(.secondary)
                        }
                        tableColumn("归属人", width: .fixed(80), tooltip: { ($0.owner?.isEmpty == false ? $0.owner! : "/") }) { (b: Bill) in
                            Text(b.owner?.isEmpty == false ? b.owner! : "/")
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }
                        tableColumn("金额", width: .fixed(90), alignment: .trailing) { (b: Bill) in
                            Text(String(format: "%.2f", b.amount))
                                .monospacedDigit()
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.horizontal, 12)
                }
            }
        }
        .task { await viewModel.load() }
    }

    private var filterPanel: some View {
        Group {
            if activeFilter == .date {
                datePanel
            } else if activeFilter == .category {
                categoryPanel
            }
        }
    }

    private var datePanel: some View {
        VStack(spacing: 16) {
            HStack(spacing: 16) {
                DatePicker("从", selection: $draftStart, in: ...draftEnd, displayedComponents: .date)
                .datePickerStyle(.graphical)
                .frame(maxWidth: .infinity)

                DatePicker("至", selection: $draftEnd, in: draftStart..., displayedComponents: .date)
                .datePickerStyle(.graphical)
                .frame(maxWidth: .infinity)
            }

            HStack(spacing: 12) {
                Button("清除筛选") {
                    draftStart = Date()
                    draftEnd = Date()
                    viewModel.filterStartDate = ""
                    viewModel.filterEndDate = ""
                    Task { await viewModel.load() }
                    activeFilter = nil
                }
                .buttonStyle(.borderless)
                .foregroundColor(.secondary)
                Button("应用") {
                    viewModel.filterStartDate = dateToString(draftStart)
                    viewModel.filterEndDate = dateToString(draftEnd)
                    Task { await viewModel.load() }
                    activeFilter = nil
                }
                .buttonStyle(.borderedProminent)
                .tint(Color("appAccent"))
            }
            .frame(maxWidth: .infinity, alignment: .center)
        }
        .padding(20)
        .frame(maxWidth: .infinity)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .shadow(color: .black.opacity(0.12), radius: 8, y: 4)
        .onAppear {
            draftStart = dateFromString(viewModel.filterStartDate)
            draftEnd = dateFromString(viewModel.filterEndDate)
        }
    }

    private var categoryPanel: some View {
        VStack(spacing: 12) {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 120), spacing: 8)], alignment: .leading, spacing: 8) {
                radioRow("全部分类", isSelected: viewModel.filterCategory.isEmpty) {
                    viewModel.filterCategory = ""
                }

                ForEach(distinctCategories, id: \.self) { cat in
                    radioRow(cat, isSelected: viewModel.filterCategory == cat) {
                        viewModel.filterCategory = cat
                    }
                }
            }

            Divider()

            HStack(spacing: 12) {
                Button("清除筛选") {
                    viewModel.filterCategory = ""
                    Task { await viewModel.load() }
                    activeFilter = nil
                }
                .buttonStyle(.borderless)
                .foregroundColor(.secondary)
                Button("应用") {
                    Task { await viewModel.load() }
                    activeFilter = nil
                }
                .buttonStyle(.borderedProminent)
                .tint(Color("appAccent"))
            }
            .frame(maxWidth: .infinity, alignment: .center)
        }
        .padding(16)
        .frame(maxWidth: .infinity)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .shadow(color: .black.opacity(0.12), radius: 8, y: 4)
    }

    private func radioRow(_ label: String, isSelected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: isSelected ? "circle.fill" : "circle")
                    .font(.system(size: 9).weight(.medium))
                    .foregroundColor(isSelected ? Color("appAccent") : .secondary)
                Text(label)
                    .font(.caption)
                    .lineLimit(1)
                    .foregroundColor(.primary)
            }
        }
        .buttonStyle(.plain)
    }

    private func dateFromString(_ str: String) -> Date {
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyy-MM-dd"
        return fmt.date(from: str) ?? Date()
    }

    private func dateToString(_ date: Date) -> String {
        let fmt = DateFormatter()
        fmt.dateFormat = "yyyy-MM-dd"
        return fmt.string(from: date)
    }

#if os(macOS)
    private func exportCSV() {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.commaSeparatedText]
        panel.nameFieldStringValue = "bills.csv"
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task {
                guard let csv = await viewModel.exportCSV(baseURL: baseURL, token: token) else { return }
                try? csv.write(to: url, atomically: true, encoding: .utf8)
            }
        }
    }
#else
    private func exportCSV() {}
#endif
}
