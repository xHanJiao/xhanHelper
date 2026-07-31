import SwiftUI
import MarkdownUI

struct DocumentListView: View {
    @StateObject var viewModel: DocumentViewModel
    @State private var selectedDocument: Document?

    var body: some View {
        if let doc = selectedDocument {
            DocumentFullView(document: doc, onBack: { selectedDocument = nil })
        } else {
            PageContainer(
                title: "文档",
                icon: "doc.text",
                isLoading: viewModel.isLoading,
                errorMessage: viewModel.errorMessage,
                isEmpty: viewModel.documents.isEmpty && !viewModel.isLoading,
                emptyMessage: "还没有文档",
                onRetry: { Task { await viewModel.load() } }
            ) {
                CardGridView(
                    items: viewModel.documents,
                    maxColumns: 1,
                    minCardWidth: 300,
                    onLoadMore: { await viewModel.loadMore() }
                ) { doc in
                    CardConfig(
                        title: doc.title,
                        subtitle: doc.content,
                        tags: parseTags(doc.tags),
                        style: .document,
                        onTap: { selectedDocument = doc }
                    )
                }
            }
            .task { await viewModel.load() }
        }
    }

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags, !tags.isEmpty else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}

private struct DocumentFullView: View {
    let document: Document
    let onBack: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Button(action: onBack) {
                    HStack(spacing: 4) {
                        Image(systemName: "chevron.left")
                            .font(.body.weight(.medium))
                        Text("返回文档列表")
                            .font(.body)
                    }
                    .foregroundColor(Color("appAccent"))
                }
                .buttonStyle(.plain)

                Spacer()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(.ultraThinMaterial.opacity(0.5))

            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Text(document.title)
                        .font(.system(size: FontSizeManager.shared.scaledSize * 1.65, weight: .semibold))

                    if let tags = document.tags, !tags.isEmpty {
                        HStack(spacing: 6) {
                            ForEach(parseTags(document.tags)) { tag in
                                Text(tag.text)
                                    .font(.system(size: FontSizeManager.shared.scaledSize * 0.76))
                                    .foregroundColor(tag.color)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 3)
                                    .background(tag.color.opacity(0.12))
                                    .clipShape(Capsule())
                            }
                        }
                    }

                    Divider()

                    Markdown(document.content)
                        .markdownTheme(.chatBubble(fontSize: FontSizeManager.shared.scaledSize * 1.18))
                        .markdownCodeSyntaxHighlighter(.plainText)
                }
                .padding(24)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private func parseTags(_ tags: String?) -> [TagBadge] {
        guard let tags else { return [] }
        return tags.split(separator: ",").map {
            TagBadge(text: String($0).trimmingCharacters(in: .whitespaces), color: Color("appAccent"))
        }
    }
}
