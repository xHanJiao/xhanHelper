import SwiftUI

struct ConversationListView: View {
    @StateObject var viewModel: ConversationListViewModel
    let onSelect: (String, String) -> Void
    let onNewChat: () -> Void

    @State private var renameTarget: Conversation?
    @State private var renameText = ""

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
                        Button(action: { onSelect(conv.id, conv.title) }) {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(conv.title)
                                    .font(.body)
                                    .lineLimit(1)
                                if let agentName = conv.agentName {
                                    Text("🤖 \(agentName)")
                                        .font(.caption2)
                                        .foregroundColor(.secondary)
                                }
                                Text(formatDate(conv.updatedAt))
                                    .font(.caption)
                                    .foregroundColor(.secondary)
                            }
                        }
                        .swipeActions(edge: .trailing) {
                            Button(role: .destructive) {
                                Task { await viewModel.delete(id: conv.id) }
                            } label: {
                                Label("删除", systemImage: "trash")
                            }
                            Button {
                                renameTarget = conv
                                renameText = conv.title
                            } label: {
                                Label("重命名", systemImage: "pencil")
                            }
                            .tint(.orange)
                        }
                    }
                }
            } header: {
                Text("历史对话")
            }
        }
        .task { await viewModel.load() }
        .refreshable { await viewModel.load() }
        .alert("重命名", isPresented: .init(
            get: { renameTarget != nil },
            set: { if !$0 { renameTarget = nil } }
        )) {
            TextField("标题", text: $renameText)
            Button("确定") {
                if let c = renameTarget {
                    Task { await viewModel.rename(id: c.id, title: renameText) }
                }
                renameTarget = nil
            }
            Button("取消", role: .cancel) { renameTarget = nil }
        } message: {
            Text("输入新标题")
        }
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
