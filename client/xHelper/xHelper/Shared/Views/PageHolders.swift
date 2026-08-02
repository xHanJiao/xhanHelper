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
    let router: NavigationRouter
    @ObservedObject var viewModel: AgentViewModel

    var body: some View {
        AgentListView(viewModel: viewModel)
            .onAppear {
                guard !router.loadedTabs.contains(.agents) else { return }
                router.markLoaded(.agents)
                Task { await viewModel.load() }
            }
            .onChange(of: router.refreshCounts[.agents] ?? 0) { _, _ in
                Task { await viewModel.load() }
            }
    }
}

struct MemoryPage: View {
    @ObservedObject var viewModel: MemoryViewModel

    var body: some View {
        MemoryListView(viewModel: viewModel)
            .task { await viewModel.load() }
    }
}

struct NotePage: View {
    @ObservedObject var viewModel: NoteViewModel

    var body: some View {
        NoteListView(viewModel: viewModel)
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
