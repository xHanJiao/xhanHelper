import SwiftUI

struct PageContainer<Content: View>: View {
    let title: String
    let icon: String
    let isLoading: Bool
    let errorMessage: String?
    let isEmpty: Bool
    let emptyMessage: String
    let onRetry: (() -> Void)?
    @ViewBuilder let content: () -> Content

    init(
        title: String,
        icon: String,
        isLoading: Bool = false,
        errorMessage: String? = nil,
        isEmpty: Bool = false,
        emptyMessage: String = "暂无数据",
        onRetry: (() -> Void)? = nil,
        @ViewBuilder content: @escaping () -> Content
    ) {
        self.title = title
        self.icon = icon
        self.isLoading = isLoading
        self.errorMessage = errorMessage
        self.isEmpty = isEmpty
        self.emptyMessage = emptyMessage
        self.onRetry = onRetry
        self.content = content
    }

    var body: some View {
        Group {
            if isLoading {
                Spacer()
                ProgressView()
                Spacer()
            } else if let error = errorMessage {
                Spacer()
                VStack(spacing: 12) {
                    Text(error).foregroundColor(.secondary)
                    if let retry = onRetry { Button("重试", action: retry) }
                }
                Spacer()
            } else if isEmpty {
                Spacer()
                VStack(spacing: 8) {
                    Image(systemName: icon).font(.largeTitle).foregroundColor(.secondary.opacity(0.5))
                    Text(emptyMessage).foregroundColor(.secondary)
                }
                Spacer()
            } else {
                content()
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
        }
    }
}
