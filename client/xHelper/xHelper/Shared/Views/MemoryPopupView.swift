import SwiftUI

struct MemoryPopupView: View {
    let memoryIDs: [String]
    let apiClient: APIClient

    @State private var memories: [UserMemory] = []
    @State private var isLoading = true

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView("加载中...")
                } else if memories.isEmpty {
                    Text("暂无记忆详情")
                        .foregroundColor(.secondary)
                } else {
                    List(memories) { mem in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(mem.memoryKey)
                                .font(.subheadline.weight(.semibold))
                            Text(mem.memoryValue)
                                .font(.body)
                                .foregroundColor(.secondary)
                            Text(mem.source)
                                .font(.caption2)
                                .foregroundColor(.secondary)
                                .padding(.top, 2)
                        }
                        .padding(.vertical, 4)
                    }
                }
            }
            .navigationTitle("🧠 基于 \(memoryIDs.count) 条回忆")
            .navigationBarTitleDisplayMode(.inline)
            .task { await load() }
        }
    }

    private func load() async {
        guard !memoryIDs.isEmpty else { isLoading = false; return }
        do {
            memories = try await apiClient.batchGetMemories(ids: memoryIDs)
        } catch {}
        isLoading = false
    }
}
