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
    let router: NavigationRouter
    @ObservedObject var viewModel: MemoryViewModel

    var body: some View {
        MemoryListView(viewModel: viewModel)
            .onAppear {
                guard !router.loadedTabs.contains(.memories) else { return }
                router.markLoaded(.memories)
                Task { await viewModel.load() }
            }
            .onChange(of: router.refreshCounts[.memories] ?? 0) { _, _ in
                Task { await viewModel.load() }
            }
    }
}

struct NotePage: View {
    let router: NavigationRouter
    @ObservedObject var viewModel: NoteViewModel

    var body: some View {
        NoteListView(viewModel: viewModel)
            .onAppear {
                guard !router.loadedTabs.contains(.notes) else { return }
                router.markLoaded(.notes)
                Task { await viewModel.load() }
            }
            .onChange(of: router.refreshCounts[.notes] ?? 0) { _, _ in
                Task { await viewModel.load() }
            }
    }
}

struct DocumentPage: View {
    let router: NavigationRouter
    @ObservedObject var viewModel: DocumentViewModel

    var body: some View {
        DocumentListView(viewModel: viewModel)
            .onAppear {
                guard !router.loadedTabs.contains(.documents) else { return }
                router.markLoaded(.documents)
                Task { await viewModel.load() }
            }
            .onChange(of: router.refreshCounts[.documents] ?? 0) { _, _ in
                Task { await viewModel.load() }
            }
    }
}

struct BillPage: View {
    let router: NavigationRouter
    @ObservedObject var viewModel: BillViewModel
    let baseURL: String
    let token: String

    var body: some View {
        BillListView(viewModel: viewModel, baseURL: baseURL, token: token)
            .onAppear {
                if !router.loadedTabs.contains(.bills) {
                    router.markLoaded(.bills)
                    Task { await viewModel.load() }
                }
                router.setToolbarActions(for: .bills, actions: [
                    ToolbarAction(id: "export", icon: "tray.and.arrow.up") {
#if os(macOS)
                        exportCSV()
#endif
                    }
                ])
            }
            .onChange(of: router.refreshCounts[.bills] ?? 0) { _, _ in
                Task { await viewModel.load() }
            }
    }

#if os(macOS)
    private func exportCSV() {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.commaSeparatedText]
        panel.nameFieldStringValue = "bills.csv"
        panel.begin { response in
            guard response == .OK, let url = panel.url else { return }
            Task {
                guard let csv = await viewModel.exportCSV(baseURL: baseURL, token: token) else { return }
                try? csv.write(to: url, atomically: true, encoding: .utf8)
            }
        }
    }
#endif
}

struct ThemePage: View {
    let router: NavigationRouter
    @ObservedObject var viewModel: ThemeViewModel

    var body: some View {
        ThemeListView(viewModel: viewModel)
            .onAppear {
                guard !router.loadedTabs.contains(.themes) else { return }
                router.markLoaded(.themes)
                Task { await viewModel.load() }
            }
            .onChange(of: router.refreshCounts[.themes] ?? 0) { _, _ in
                Task { await viewModel.load() }
            }
    }
}
