import SwiftUI
#if os(macOS)
import AppKit
import UniformTypeIdentifiers
#endif

struct ChatPage: View {
    @ObservedObject var chatViewModel: ChatViewModel
    let loadError: String?
    let baseURL: String
    let token: String
    /// 只有当前会话 tab 响应空格按住；PiP 副本传 false
    var isCurrentTab: Bool = false

    var body: some View {
        Group {
            if let msg = loadError, chatViewModel.messages.isEmpty {
                VStack(spacing: 12) {
                    Text(msg).foregroundColor(.secondary)
                }
            } else {
                ChatView(viewModel: chatViewModel, agentId: nil,
                         baseURL: baseURL, token: token, isCurrentTab: isCurrentTab)
            }
        }
    }
}

struct AgentPage: View {
    @ObservedObject var viewModel: AgentViewModel

    var body: some View {
        AgentListView(viewModel: viewModel)
            .task { await viewModel.load() }
    }
}

struct MemoryPage: View {
    @ObservedObject var viewModel: MemoryViewModel

    var body: some View {
        // 加载由 MemoryListView 自己的 .task 负责（列表/词云两分支都覆盖）
        MemoryListView(viewModel: viewModel)
    }
}

struct ArtifactPage: View {
    @ObservedObject var viewModel: ArtifactViewModel

    var body: some View {
        ArtifactListView(viewModel: viewModel)
    }
}

struct DocumentPage: View {
    @ObservedObject var viewModel: DocumentViewModel
    /// 卡片点开详情：统一 tab 后详情是独立 tab，由 manager 开
    var onOpen: ((Document) -> Void)? = nil

    var body: some View {
        DocumentListView(viewModel: viewModel, onOpen: onOpen)
            .task { await viewModel.load() }
    }
}

struct BillPage: View {
    @ObservedObject var viewModel: BillViewModel
    let baseURL: String
    let token: String

    var body: some View {
        BillListView(viewModel: viewModel, baseURL: baseURL, token: token)
            .task { await viewModel.load() }
    }
}

struct EventPage: View {
    @ObservedObject var viewModel: EventViewModel

    var body: some View {
        // 加载由 EventListView 自己的 .task 负责，避免进入面板时并发打两次列表请求。
        EventListView(viewModel: viewModel)
    }
}

struct ImagePage: View {
    @ObservedObject var viewModel: ImageViewModel
    let baseURL: String
    let token: String

    var body: some View {
        ImageListView(viewModel: viewModel, baseURL: baseURL, token: token)
    }
}
