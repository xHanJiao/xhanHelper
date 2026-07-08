import SwiftUI

struct MemoryPopupView: View {
    let memoryIDs: [String]
    let apiClient: APIClient

    @State private var memories: [UserMemory] = []
    @State private var isLoading = true
    @State private var errorMessage: String?

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView("加载中...")
                } else if let err = errorMessage {
                    VStack(spacing: 12) {
                        Text(err).foregroundColor(.secondary)
                        Button("重试") { Task { await load() } }
                    }
                } else if memories.isEmpty {
                    Text("暂无记忆详情")
                        .foregroundColor(.secondary)
                } else {
                    List(memories) { mem in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(mem.topic ?? "记忆")
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
            errorMessage = nil
        } catch {
            LogBuffer.shared.error("MemoryPopupView.load: \(error)")
            errorMessage = "加载失败: \(error.localizedDescription)"
        }
        isLoading = false
    }
}
