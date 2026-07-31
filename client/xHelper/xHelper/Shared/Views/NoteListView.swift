import SwiftUI

struct NoteListView: View {
    @StateObject var viewModel: NoteViewModel
    @State private var selectedNote: Note?

    var body: some View {
        PageContainer(
            title: "笔记",
            icon: "note.text",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.notes.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有笔记，去聊天页面让 AI 帮你记录吧",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.notes,
                maxColumns: 3,
                minCardWidth: 260,
                onLoadMore: { await viewModel.loadMore() }
            ) { note in
                CardConfig(
                    title: note.topic,
                    subtitle: note.content,
                    tags: parseTags(note.tags),
                    style: .note,
                    onTap: { selectedNote = note }
                )
            }
        }
        .task { await viewModel.load() }
        .overlay {
            if let note = selectedNote {
                NoteDetailOverlay(note: note, onClose: { selectedNote = nil })
            }
        }
    }

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}
