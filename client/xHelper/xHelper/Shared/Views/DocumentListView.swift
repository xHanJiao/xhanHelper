import SwiftUI

struct DocumentListView: View {
    let apiClient: APIClient
    @State private var documents: [Document] = []
    @State private var isLoading = true
    @State private var total = 0
    @State private var errorMessage: String?
    private let pageSize = 24

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if let err = errorMessage {
                VStack(spacing: 12) {
                    Text(err).foregroundColor(.secondary)
                    Button("重试") { Task { await load() } }
                }
            } else if documents.isEmpty {
                Text("暂无文档")
                    .foregroundColor(.secondary)
            } else {
                List {
                    ForEach(documents) { doc in
                        NavigationLink(destination: DocumentDetailView(document: doc)) {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(doc.title)
                                    .font(.subheadline.weight(.medium))
                                HStack {
                                    if let tagText = doc.tags, !tagText.isEmpty {
                                        Text(tagText.components(separatedBy: ",").first?.trimmingCharacters(in: .whitespaces) ?? tagText)
                                            .font(.caption)
                                            .foregroundColor(.blue)
                                    }
                                    Spacer()
                                    Text(formatDate(doc.updatedAt))
                                        .font(.caption2)
                                        .foregroundColor(.secondary)
                                }
                            }
                            .padding(.vertical, 2)
                        }
                        .swipeActions(edge: .trailing) {
                            Button("删除", role: .destructive) {
                                Task { await delete(doc) }
                            }
                        }
                    }

                    if documents.count < total {
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
        .navigationTitle("文档")
        .task { await load() }
    }

    private func load() async {
        do {
            let resp = try await apiClient.listDocuments(limit: pageSize, offset: 0)
            documents = resp.documents
            total = resp.total
            errorMessage = nil
        } catch {
            LogBuffer.shared.error("DocumentListView.load: \(error)")
            errorMessage = "加载失败: \(error.localizedDescription)"
        }
        isLoading = false
    }

    private func loadMore() async {
        let newOffset = documents.count
        guard newOffset < total else { return }
        do {
            let resp = try await apiClient.listDocuments(limit: pageSize, offset: newOffset)
            documents.append(contentsOf: resp.documents)
        } catch {
            LogBuffer.shared.error("DocumentListView.loadMore: \(error)")
        }
    }

    private func delete(_ doc: Document) async {
        do {
            try await apiClient.deleteDocument(id: doc.id)
            documents.removeAll { $0.id == doc.id }
            total -= 1
        } catch {
            LogBuffer.shared.error("DocumentListView.delete: \(error)")
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
