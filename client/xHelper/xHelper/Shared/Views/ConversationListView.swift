import SwiftUI

struct ConversationListView: View {
    @StateObject var viewModel: ConversationListViewModel
    let onSelect: (String) -> Void
    let onNewChat: () -> Void

    var body: some View {
        List {
            Section {
                Button(action: onNewChat) {
                    Label("新建对话", systemImage: "square.and.pencil")
                }
            }

            Section {
                if viewModel.conversations.isEmpty {
                    Text("暂无对话")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                } else {
                    ForEach(viewModel.conversations) { conv in
                        Button(action: { onSelect(conv.id) }) {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(conv.title)
                                    .font(.body)
                                    .lineLimit(1)
                                Text(formatDate(conv.updatedAt))
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                            }
                        }
                    }
                }
            } header: {
                Text("历史对话")
            }
        }
        .task { await viewModel.load() }
        .refreshable { await viewModel.load() }
    }

    private func formatDate(_ iso: String) -> String {
        let fmt = ISO8601DateFormatter()
        fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let date = fmt.date(from: iso) ?? ISO8601DateFormatter().date(from: iso) else {
            return iso
        }
        let relative = RelativeDateTimeFormatter()
        relative.locale = Locale(identifier: "zh_CN")
        return relative.localizedString(for: date, relativeTo: Date())
    }
}
