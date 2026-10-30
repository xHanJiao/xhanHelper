import SwiftUI

struct AgentListView: View {
    @StateObject var viewModel: AgentViewModel
    @State private var selectedAgent: Agent?
    /// 待确认删除的智能体（设定不可恢复，走二次确认）
    @State private var pendingDelete: Agent?

    @State private var editName = ""
    @State private var editPrompt = ""
    @State private var createName = ""
    @State private var createPrompt = ""

    var body: some View {
        PageContainer(
            title: "智能体",
            icon: "cpu",
            isLoading: viewModel.isLoading,
            errorMessage: viewModel.errorMessage,
            isEmpty: viewModel.agents.isEmpty && !viewModel.isLoading,
            emptyMessage: "还没有智能体",
            onRetry: { Task { await viewModel.load() } }
        ) {
            CardGridView(
                items: viewModel.agents,
                maxColumns: 1,
                minCardWidth: 300,
                onLoadMore: nil
            ) { agent in
                CardConfig(
                    title: agent.name,
                    subtitle: agent.systemPrompt,
                    style: .agent,
                    // 与文档/灵阅卡片同款：管理类动作收进右上角「⋯」，页脚不放按钮
                    menuActions: [
                        CardAction(label: "编辑", icon: "pencil", role: .normal) {
                            viewModel.editingAgent = agent
                            editName = agent.name
                            editPrompt = agent.systemPrompt
                        },
                        CardAction(label: "删除", icon: "trash", role: .destructive) {
                            pendingDelete = agent
                        },
                    ],
                    onTap: { selectedAgent = agent }
                )
            }
        }
        .task { await viewModel.load() }
        .overlay {
            if let agent = selectedAgent {
                AgentDetailOverlay(agent: agent, onClose: { selectedAgent = nil })
            }
        }
        .onChange(of: viewModel.showCreateSheet) { _, new in
            if !new { createName = ""; createPrompt = "" }
        }
        .onChange(of: viewModel.editingAgent?.id) { _, _ in
            if viewModel.editingAgent == nil { editName = ""; editPrompt = "" }
        }
        .confirmationDialog(
            "删除智能体",
            isPresented: Binding(
                get: { pendingDelete != nil },
                set: { if !$0 { pendingDelete = nil } }
            ),
            titleVisibility: .visible,
            presenting: pendingDelete
        ) { agent in
            Button("删除", role: .destructive) {
                Task { await viewModel.delete(id: agent.id) }
            }
            Button("取消", role: .cancel) { }
        } message: { agent in
            Text("「\(agent.name)」及其设定将被删除，不能恢复。")
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
            Section("设定") {
                TextEditor(text: prompt)
                    .frame(minHeight: 180)
                    .scrollContentBackground(.hidden)
                Text("\(prompt.wrappedValue.count)/3000")
                    .font(.caption)
                    .foregroundColor(prompt.wrappedValue.count > 3000 ? .red : .secondary)
            }
        }
        .navigationTitle(title)
#if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
#endif
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("取消") { onDismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button("保存") { Task { await onSubmit() } }
                    .disabled(name.wrappedValue.trimmingCharacters(in: .whitespaces).isEmpty
                        || prompt.wrappedValue.trimmingCharacters(in: .whitespaces).isEmpty
                        || prompt.wrappedValue.count > 3000)
            }
        }
    }
}
