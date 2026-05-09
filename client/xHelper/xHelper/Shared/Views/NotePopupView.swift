import SwiftUI

struct NotePopupView: View {
    let noteIDs: [String]
    let apiClient: APIClient

    @State private var notes: [Note] = []
    @State private var isLoading = true

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView("加载中...")
                } else if notes.isEmpty {
                    Text("暂无笔记详情")
                        .foregroundColor(.secondary)
                } else {
                    List(notes) { note in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(note.topic)
                                .font(.subheadline.weight(.semibold))
                            Text(note.content)
                                .font(.body)
                                .foregroundColor(.secondary)
                                .lineLimit(3)
                            if let tags = note.tags, !tags.isEmpty {
                                Text(tags)
                                    .font(.caption2)
                                    .foregroundColor(.secondary)
                                    .padding(.top, 2)
                            }
                        }
                        .padding(.vertical, 4)
                    }
                }
            }
            .navigationTitle("📝 基于 \(noteIDs.count) 条笔记")
            .navigationBarTitleDisplayMode(.inline)
            .task { await load() }
        }
    }

    private func load() async {
        guard !noteIDs.isEmpty else { isLoading = false; return }
        do {
            notes = try await apiClient.batchGetNotes(ids: noteIDs)
        } catch {}
        isLoading = false
    }
}
