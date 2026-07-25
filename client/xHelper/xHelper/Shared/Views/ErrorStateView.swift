import SwiftUI

struct ErrorStateView: View {
    let message: String
    let systemImage: String
    let retryLabel: String
    let onRetry: (() -> Void)?

    init(
        message: String,
        systemImage: String = "wifi.slash",
        retryLabel: String = "点击重试",
        onRetry: (() -> Void)? = nil
    ) {
        self.message = message
        self.systemImage = systemImage
        self.retryLabel = retryLabel
        self.onRetry = onRetry
    }

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: systemImage)
                .font(.system(size: 36))
                .foregroundColor(.secondary)

            Text(message)
                .font(.body)
                .foregroundColor(.secondary)
                .multilineTextAlignment(.center)

            if let onRetry {
                Button(action: onRetry) {
                    Label(retryLabel, systemImage: "arrow.clockwise")
                        .font(.subheadline)
                }
                .buttonStyle(.bordered)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(32)
    }
}

struct MinimumLoadingView<Content: View>: View {
    let isLoading: Bool
    let errorMessage: String?
    let minDuration: TimeInterval
    let onRetry: (() -> Void)?
    @ViewBuilder let content: () -> Content

    @State private var phase: Phase = .loading
    @State private var loadStartTime: Date?

    private enum Phase {
        case loading
        case error
        case content
    }

    init(
        isLoading: Bool,
        errorMessage: String?,
        minDuration: TimeInterval = 1.0,
        onRetry: (() -> Void)? = nil,
        @ViewBuilder content: @escaping () -> Content
    ) {
        self.isLoading = isLoading
        self.errorMessage = errorMessage
        self.minDuration = minDuration
        self.onRetry = onRetry
        self.content = content
    }

    var body: some View {
        Group {
            switch phase {
            case .loading:
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .error:
                if let errorMessage {
                    ErrorStateView(message: errorMessage, onRetry: onRetry)
                }
            case .content:
                content()
            }
        }
        .onChange(of: isLoading) { _, loading in
            if loading {
                phase = .loading
                loadStartTime = Date()
            } else {
                completeLoading()
            }
        }
        .onAppear {
            if isLoading {
                loadStartTime = Date()
                phase = .loading
            } else if errorMessage != nil {
                phase = .error
            } else {
                phase = .content
            }
        }
    }

    private func completeLoading() {
        Task {
            if let start = loadStartTime {
                let elapsed = Date().timeIntervalSince(start)
                let wait = max(0, minDuration - elapsed)
                if wait > 0 {
                    try? await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000))
                }
            }
            await MainActor.run {
                if let errorMessage, !errorMessage.isEmpty {
                    phase = .error
                } else {
                    phase = .content
                }
            }
        }
    }
}

struct InlineRetryBar: View {
    let message: String
    let action: () -> Void

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "exclamationmark.circle")
            Text(message)
                .font(.caption)
        }
        .foregroundColor(.red.opacity(0.8))
        .padding(.vertical, 8)
        .onTapGesture(perform: action)
    }
}

struct LoadMoreHeader: View {
    let isLoading: Bool
    let errorMessage: String?
    let hasMore: Bool
    let minDuration: TimeInterval
    let loadingLabel: LoadMoreLabel
    let moreLabel: LoadMoreLabel
    let noMoreLabel: LoadMoreLabel
    let onLoadMore: () -> Void

    @State private var stickySpinner = false
    @State private var loadStartTime: Date?

    init(
        isLoading: Bool,
        errorMessage: String?,
        hasMore: Bool,
        minDuration: TimeInterval = 1.0,
        loadingLabel: LoadMoreLabel = .loading,
        moreLabel: LoadMoreLabel = .more,
        noMoreLabel: LoadMoreLabel = .noMore,
        onLoadMore: @escaping () -> Void
    ) {
        self.isLoading = isLoading
        self.errorMessage = errorMessage
        self.hasMore = hasMore
        self.minDuration = minDuration
        self.loadingLabel = loadingLabel
        self.moreLabel = moreLabel
        self.noMoreLabel = noMoreLabel
        self.onLoadMore = onLoadMore
    }

    var body: some View {
        Group {
            if stickySpinner {
                HStack {
                    ProgressView().scaleEffect(0.7)
                    Text(loadingLabel.text)
                        .font(loadingLabel.font)
                        .foregroundColor(loadingLabel.color)
                }
            } else if let errorMessage {
                InlineRetryBar(message: errorMessage, action: onLoadMore)
            } else if hasMore {
                HStack {
                    Image(systemName: "arrow.up")
                    Text(moreLabel.text)
                        .font(moreLabel.font)
                        .foregroundColor(moreLabel.color)
                }
                .onTapGesture(perform: onLoadMore)
            } else {
                Text(noMoreLabel.text)
                    .font(noMoreLabel.font)
                    .foregroundColor(noMoreLabel.color)
            }
        }
        .onChange(of: isLoading) { _, loading in
            if loading {
                stickySpinner = true
                loadStartTime = Date()
            } else {
                completeLoading()
            }
        }
    }

    private func completeLoading() {
        Task {
            if let start = loadStartTime {
                let elapsed = Date().timeIntervalSince(start)
                let wait = max(0, minDuration - elapsed)
                if wait > 0 {
                    try? await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000))
                }
            }
            await MainActor.run {
                stickySpinner = false
            }
        }
    }
}

struct LoadMoreLabel {
    let text: String
    let font: Font
    let color: Color

    static let loading = LoadMoreLabel(
        text: "正在加载...",
        font: .caption,
        color: Color("accentSecondary")
    )

    static let more = LoadMoreLabel(
        text: "加载更多内容",
        font: .caption,
        color: Color("accentSecondary")
    )

    static let noMore = LoadMoreLabel(
        text: "已无更多内容",
        font: .caption,
        color: .secondary
    )
}
