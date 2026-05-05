import MarkdownUI
import SwiftUI

struct AgentListView: View {
    @StateObject var viewModel: AgentViewModel
    @State private var selectedAgent: Agent?

    @State private var editName = ""
    @State private var editPrompt = ""
    @State private var createName = ""
    @State private var createPrompt = ""

    var body: some View {
        List {
            if viewModel.agents.isEmpty && !viewModel.isLoading {
                Text("还没有智能体，点击右上角 + 创建")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
            } else {
                ForEach(viewModel.agents) { agent in
                    HStack {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(agent.name)
                                .font(.subheadline.weight(.medium))
                        }
                        Spacer()
                        Button {
                            viewModel.chatWithAgent(agent)
                        } label: {
                            Image(systemName: "bubble.left.and.bubble.right")
                                .font(.body)
                                .foregroundColor(.blue)
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.vertical, 4)
                    .contentShape(Rectangle())
                    .onTapGesture { selectedAgent = agent }
                    .swipeActions(edge: .trailing) {
                        Button(role: .destructive) {
                            Task { await viewModel.delete(id: agent.id) }
                        } label: {
                            Label("删除", systemImage: "trash")
                        }
                    }
                    .swipeActions(edge: .leading) {
                        Button {
                            viewModel.editingAgent = agent
                            editName = agent.name
                            editPrompt = agent.systemPrompt
                        } label: {
                            Label("编辑", systemImage: "pencil")
                        }
                        .tint(.orange)
                    }
                }
            }
        }
        .navigationTitle("智能体")
        .dismissKeyboardOnTap()
        .toolbar {
            Button { viewModel.showCreateSheet = true } label: {
                Image(systemName: "plus")
            }
        }
        .task { await viewModel.load() }
        .refreshable { await viewModel.load() }
        .onChange(of: viewModel.showCreateSheet) { _, new in if !new { createName = ""; createPrompt = "" } }
        .onChange(of: viewModel.editingAgent?.id) { _, _ in
            if viewModel.editingAgent == nil { editName = ""; editPrompt = "" }
        }
        .sheet(item: $selectedAgent) { agent in
            NavigationStack {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        Text(agent.name)
                            .font(.title2.weight(.semibold))
                        Markdown(agent.systemPrompt)
                            .markdownTheme(.gitHub)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .markdownCodeSyntaxHighlighter(.plainText)
                    }
                    .padding()
                }
                .navigationTitle("智能体详情")
                .navigationBarTitleDisplayMode(.inline)
            }
        }
        .sheet(isPresented: $viewModel.showCreateSheet) {
            agentForm(title: "创建智能体", name: $createName, prompt: $createPrompt,
                onDismiss: { viewModel.showCreateSheet = false },
                onSubmit: { await viewModel.create(name: createName, systemPrompt: createPrompt) }
            )
        }
        .sheet(item: $viewModel.editingAgent) { agent in
            agentForm(title: "编辑智能体", name: $editName, prompt: $editPrompt,
                onDismiss: { viewModel.editingAgent = nil },
                onSubmit: { await viewModel.update(agent: agent, name: editName, systemPrompt: editPrompt) }
            )
        }
    }
}

private func agentForm(title: String, name: Binding<String>, prompt: Binding<String>, onDismiss: @escaping () -> Void, onSubmit: @escaping () async -> Void) -> some View {
    NavigationStack {
        Form {
            Section("名称") {
                TextField("智能体名称", text: name)
            }
            Section("智能体设定") {
                TextEditor(text: prompt)
                    .frame(minHeight: 150)
                Text("\(prompt.wrappedValue.count)/2000")
                    .font(.caption)
                    .foregroundColor(prompt.wrappedValue.count > 2000 ? .red : .secondary)
            }
        }
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("取消") { onDismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button("保存") {
                    Task { await onSubmit() }
                }
                .disabled(name.wrappedValue.trimmingCharacters(in: .whitespaces).isEmpty
                    || prompt.wrappedValue.trimmingCharacters(in: .whitespaces).isEmpty
                    || prompt.wrappedValue.count > 2000)
            }
        }
    }
}
