import SwiftUI

struct MemoryListView: View {
    @StateObject var viewModel: MemoryViewModel
    @State private var searchText = ""
    @State private var selectedMemory: UserMemory?

    var body: some View {
        List {
            if viewModel.memories.isEmpty && !viewModel.isLoading {
                Text("还没有记忆，去聊天页面和 AI 聊聊吧")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
            } else {
                ForEach(viewModel.memories) { mem in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(mem.memoryValue)
                            .font(.body)
                            .lineLimit(2)
                            .foregroundColor(.secondary)
                        HStack {
                            Text(mem.source)
                                .font(.caption2)
                                .foregroundColor(.secondary)
                            Spacer()
                            Text(formatMemoryDate(mem.createdAt))
                                .font(.caption2)
                                .foregroundColor(.secondary)
                        }
                    }
                    .padding(.vertical, 4)
                    .contentShape(Rectangle())
                    .onTapGesture { selectedMemory = mem }
                    .onAppear {
                        if mem.id == viewModel.memories.last?.id {
                            Task { await viewModel.loadMore() }
                        }
                    }
                    .swipeActions {
                        Button(role: .destructive) {
                            Task { await viewModel.delete(id: mem.id) }
                        } label: {
                            Label("删除", systemImage: "trash")
                        }
                    }
                }
            }
        }
        .searchable(text: $searchText, prompt: "搜索记忆")
        .onChange(of: searchText) {
            viewModel.searchKeyword = searchText
            Task { await viewModel.load() }
        }
        .refreshable { await viewModel.load() }
        .task { await viewModel.load() }
        .navigationTitle("记忆")
        .alert("操作失败", isPresented: .init(get: { viewModel.errorMessage != nil }, set: { if !$0 { viewModel.errorMessage = nil } })) {
            Button("确定") {}
        } message: {
            Text(viewModel.errorMessage ?? "")
        }
        .sheet(item: $selectedMemory) { mem in
            NavigationStack {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        Text(mem.memoryValue)
                            .font(.body)
                        Label(mem.source, systemImage: "tag")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    .padding()
                }
                .navigationTitle("记忆详情")
                .navigationBarTitleDisplayMode(.inline)
            }
        }
    }

    private func formatMemoryDate(_ iso: String) -> String {
        let fmt = ISO8601DateFormatter()
        fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let date = fmt.date(from: iso) ?? ISO8601DateFormatter().date(from: iso) else {
            return iso
        }
        let df = DateFormatter()
        df.dateFormat = "MM-dd HH:mm"
        return df.string(from: date)
    }
}
