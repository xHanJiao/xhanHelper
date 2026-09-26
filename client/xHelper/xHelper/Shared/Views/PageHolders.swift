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

    var body: some View {
        Group {
            if let msg = loadError, chatViewModel.messages.isEmpty {
                VStack(spacing: 12) {
                    Text(msg).foregroundColor(.secondary)
                }
            } else {
                ChatView(viewModel: chatViewModel, agentId: nil,
                         baseURL: baseURL, token: token)
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

struct NotePage: View {
    @ObservedObject var viewModel: NoteViewModel

    var body: some View {
        NoteListView(viewModel: viewModel)
            .task { await viewModel.load() }
    }
}

struct FactPage: View {
    @ObservedObject var viewModel: FactViewModel

    var body: some View {
        FactListView(viewModel: viewModel)
            .task { await viewModel.load() }
    }
}

struct DocumentPage: View {
    @ObservedObject var viewModel: DocumentViewModel

    var body: some View {
        DocumentListView(viewModel: viewModel)
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

struct ThemePage: View {
    @ObservedObject var viewModel: ThemeViewModel

    var body: some View {
        ThemeListView(viewModel: viewModel)
            .task { await viewModel.load() }
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
