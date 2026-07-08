import SwiftUI

struct NoteListView: View {
    let apiClient: APIClient
    @State private var notes: [Note] = []
    @State private var isLoading = true
    @State private var total = 0
    @State private var selectedNote: Note?
    @State private var errorMessage: String?
    private let pageSize = 50

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if let err = errorMessage {
                VStack(spacing: 12) {
                    Text(err).foregroundColor(.secondary)
                    Button("重试") { Task { await load() } }
                }
            } else if notes.isEmpty {
                Text("暂无笔记")
                    .foregroundColor(.secondary)
            } else {
                List {
                    ForEach(notes) { note in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                Text(note.topic)
                                    .font(.subheadline.weight(.medium))
                                Spacer()
                                Text(formatDate(note.createdAt))
                                    .font(.caption2)
                                    .foregroundColor(.secondary)
                            }
                            Text(note.content)
                                .font(.body)
                                .lineLimit(3)
                                .foregroundColor(.secondary)
                        }
                        .padding(.vertical, 2)
                        .contentShape(Rectangle())
                        .onTapGesture { selectedNote = note }
                    }

                    if notes.count < total {
                        HStack {
                            Spacer()
                            ProgressView()
                                .padding()
                                .task { await loadMore() }
                            Spacer()
                        }
                    }
                }
            }
        }
        .navigationTitle("笔记记录")
        .sheet(item: $selectedNote) { note in
            NavigationStack {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        Text(note.topic)
                            .font(.title2.weight(.semibold))
                        Text(note.content)
                            .font(.body)
                    }
                    .padding()
                }
                .navigationTitle("笔记详情")
                .navigationBarTitleDisplayMode(.inline)
            }
        }
        .task { await load() }
    }

    private func load() async {
        do {
            let resp = try await apiClient.listNotes(limit: pageSize, offset: 0)
            notes = resp.notes
            total = resp.total
            errorMessage = nil
        } catch {
            LogBuffer.shared.error("NoteListView.load: \(error)")
            errorMessage = "加载失败: \(error.localizedDescription)"
        }
        isLoading = false
    }

    private func loadMore() async {
        let newOffset = notes.count
        guard newOffset < total else { return }
        do {
            let resp = try await apiClient.listNotes(limit: pageSize, offset: newOffset)
            notes.append(contentsOf: resp.notes)
        } catch {
            LogBuffer.shared.error("NoteListView.loadMore: \(error)")
        }
    }

    private func formatDate(_ iso: String) -> String {
        let fmt = ISO8601DateFormatter()
        fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let date = fmt.date(from: iso) ?? ISO8601DateFormatter().date(from: iso) else { return iso }
        let df = DateFormatter()
        df.dateFormat = "MM-dd HH:mm"
        return df.string(from: date)
    }
}
