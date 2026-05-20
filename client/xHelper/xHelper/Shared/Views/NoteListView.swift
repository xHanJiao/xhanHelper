import SwiftUI

struct NoteListView: View {
    let apiClient: APIClient
    @State private var notes: [Note] = []
    @State private var isLoading = true
    @State private var selectedNote: Note?

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if notes.isEmpty {
                Text("暂无笔记")
                    .foregroundColor(.secondary)
            } else {
                List(notes) { note in
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
            let resp = try await apiClient.listNotes()
            notes = resp.notes
        } catch {}
        isLoading = false
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
