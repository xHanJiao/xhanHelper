import SwiftUI

enum FilterType { case date, category, owner, stats }

struct BillListView: View {
    @StateObject var viewModel: BillViewModel

    let baseURL: String
    let token: String

    @State private var activeFilter: FilterType?
    @State private var draftStart = Date()
    @State private var draftEnd = Date()

    private var hasActiveFilter: Bool {
        !viewModel.filterStartDate.isEmpty
            || !viewModel.filterEndDate.isEmpty
            || !viewModel.filterCategoryList.isEmpty
            || !viewModel.filterOwner.isEmpty
            || !viewModel.keyword.isEmpty
    }

    /// 统计面板的刷新指纹：筛选/关键词变化时自动重拉（面板开着也保持准确）。
    private var statsKey: String {
        [
            viewModel.filterStartDate,
            viewModel.filterEndDate,
            viewModel.filterCategoryList.joined(separator: ","),
            viewModel.filterOwner,
            viewModel.keyword,
        ].joined(separator: "|")
    }

    /// 筛选后无结果时的覆盖提示：保留表头筛选入口（可继续调整），并提供一键清除。
    private var filteredEmptyOverlay: some View {
        VStack(spacing: 8) {
            Image(systemName: "line.3.horizontal.decrease.circle")
                .font(.system(size: 22))
                .foregroundColor(.secondary.opacity(0.6))
            Text("没有符合条件的账单")
                .font(.system(size: 13, weight: .medium))
            Text("点上方表头图标调整日期/分类，或在页头🔍更换关键词")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
            Button("清除筛选") { clearAllFilters() }
                .buttonStyle(.bordered)
                .controlSize(.small)
        }
        .multilineTextAlignment(.center)
        .padding(20)
        .frame(maxWidth: .infinity)
    }

    private func clearAllFilters() {
        viewModel.filterStartDate = ""
        viewModel.filterEndDate = ""
        viewModel.filterCategoryList = []
        viewModel.filterOwner = ""
        viewModel.keyword = ""
        activeFilter = nil
        Task { await viewModel.load() }
    }

    var body: some View {
        PageContainer(
            title: "账单",
            icon: "yensign.circle",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.bills.isEmpty && !viewModel.isLoading && !hasActiveFilter,
            emptyMessage: "还没有账单记录",
            onRetry: { Task { await viewModel.load() } }
        ) {
            VStack(spacing: 0) {
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
                                        .foregroundColor(viewModel.filterCategoryList.isEmpty ? .secondary : Color("appAccent"))
                                }
                                .buttonStyle(.plain)
                                .help("分类筛选")
                            )
                        }) { (b: Bill) in
                            Text(b.category2.isEmpty ? b.category1 : "\(b.category1)/\(b.category2)")
                                .font(.caption).foregroundColor(.secondary)
                        }
                        tableColumn("归属人", width: .fixed(100), tooltip: { ($0.owner?.isEmpty == false ? $0.owner! : "/") }, headerAccessory: { _ in
                            AnyView(
                                Button {
                                    activeFilter = (activeFilter == .owner) ? nil : .owner
                                } label: {
                                    Image(systemName: "line.3.horizontal.decrease.circle")
                                        .font(.system(size: 10).weight(.medium))
                                        .foregroundColor(viewModel.filterOwner.isEmpty ? .secondary : Color("appAccent"))
                                }
                                .buttonStyle(.plain)
                                .help("归属人筛选")
                            )
                        }) { (b: Bill) in
                            Text(b.owner?.isEmpty == false ? b.owner! : "/")
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }
                        tableColumn("备注", tooltip: { $0.remark?.isEmpty == false ? $0.remark! : "/" }) { (b: Bill) in
                            Text(b.remark?.isEmpty == false ? b.remark! : "/")
                                .font(.caption)
                                .foregroundColor(.secondary)
                                .lineLimit(1)
                        }
                        tableColumn("金额", width: .fixed(90), headerAccessory: { _ in
                            AnyView(
                                Button {
                                    activeFilter = (activeFilter == .stats) ? nil : .stats
                                } label: {
                                    Image(systemName: "sum")
                                        .font(.system(size: 10).weight(.medium))
                                        .foregroundColor(activeFilter == .stats ? Color("appAccent") : .secondary)
                                }
                                .buttonStyle(.plain)
                                .help("金额统计")
                            )
                        }) { (b: Bill) in
                            Text(String(format: "%.2f", b.amount))
                                .monospacedDigit()
                        }
                    }
                    .overlay {
                        if viewModel.bills.isEmpty && !viewModel.isLoading && activeFilter == nil {
                            filteredEmptyOverlay
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.horizontal, 12)
            }
        }
        .task { await viewModel.loadIfStale() }
    }

    private var filterPanel: some View {
        Group {
            if activeFilter == .date {
                datePanel
            } else if activeFilter == .category {
                categoryPanel
            } else if activeFilter == .owner {
                ownerPanel
            } else if activeFilter == .stats {
                statsPanel
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
            if viewModel.categoryOptions.isEmpty {
                Text("暂无分类数据")
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .padding(.vertical, 8)
            } else {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 120), spacing: 8)], alignment: .leading, spacing: 8) {
                    checkRow("全部分类", isChecked: viewModel.filterCategoryList.isEmpty) {
                        viewModel.filterCategoryList = []
                    }

                    ForEach(viewModel.categoryOptions, id: \.self) { cat in
                        checkRow(cat, isChecked: viewModel.filterCategoryList.contains(cat)) {
                            viewModel.toggleCategory(cat)
                        }
                    }
                }
            }

            Divider()

            HStack(spacing: 12) {
                Button("清除筛选") {
                    viewModel.filterCategoryList = []
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
        .task { await viewModel.loadCategories() }
    }

    private var ownerPanel: some View {
        VStack(spacing: 12) {
            if viewModel.ownerOptions.isEmpty {
                Text("暂无归属人数据")
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .padding(.vertical, 8)
            } else {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 120), spacing: 8)], alignment: .leading, spacing: 8) {
                    radioRow("全部人员", isSelected: viewModel.filterOwner.isEmpty) {
                        viewModel.filterOwner = ""
                    }

                    ForEach(viewModel.ownerOptions, id: \.self) { owner in
                        radioRow(owner, isSelected: viewModel.filterOwner == owner) {
                            viewModel.filterOwner = owner
                        }
                    }
                }
            }

            Divider()

            HStack(spacing: 12) {
                Button("清除筛选") {
                    viewModel.filterOwner = ""
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
        .task { await viewModel.loadOwners() }
    }

    private func checkRow(_ label: String, isChecked: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: isChecked ? "checkmark.square.fill" : "square")
                    .font(.system(size: 11).weight(.medium))
                    .foregroundColor(isChecked ? Color("appAccent") : .secondary)
                Text(label)
                    .font(.caption)
                    .lineLimit(1)
                    .foregroundColor(.primary)
            }
        }
        .buttonStyle(.plain)
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

    private var statsPanel: some View {
        VStack(spacing: 12) {
            HStack(spacing: 20) {
                VStack(alignment: .leading, spacing: 3) {
                    Text("笔数")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    Text("\(viewModel.billStats?.count ?? 0)")
                        .font(.system(size: 18, weight: .medium))
                        .monospacedDigit()
                }
                VStack(alignment: .leading, spacing: 3) {
                    Text("合计")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    Text(String(format: "¥%.2f", viewModel.billStats?.sum ?? 0))
                        .font(.system(size: 18, weight: .semibold))
                        .foregroundColor(Color("appAccent"))
                        .monospacedDigit()
                }
                Spacer()
                if viewModel.isLoadingStats {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Text("统计当前筛选全集（含未加载页）")
                .font(.caption2)
                .foregroundColor(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(16)
        .frame(maxWidth: .infinity)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
        .shadow(color: .black.opacity(0.12), radius: 8, y: 4)
        .task(id: statsKey) {
            await viewModel.loadStats()
        }
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
}
