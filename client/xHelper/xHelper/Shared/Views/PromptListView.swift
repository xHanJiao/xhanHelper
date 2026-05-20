import SwiftUI
#if os(iOS)
import UIKit
#endif

private let defaultSystemPrompt = """
你是 xhanHelper，一个个人 AI 助手。你的职责是帮助用户高效完成任务。

核心原则：
1. 简洁回复：用最少的字把事说清楚
2. 诚实：不知道就说不知道，不要编造
3. 主动：如果用户意图不明确，追问关键信息
"""

struct PromptListView: View {
    @StateObject var viewModel: PromptViewModel
    @State private var showEditor = false
    @State private var editingType = "flash"
    @State private var editingPrompt: UserPrompt?
    @State private var showDefaultPrompt = false

    var body: some View {
        List {
            Section {
                DisclosureGroup(isExpanded: $showDefaultPrompt) {
                    Text(defaultSystemPrompt)
                        .font(.callout)
                        .foregroundColor(.secondary)
                        .padding(.vertical, 4)
                        .contextMenu {
                            Button {
                                #if os(iOS)
                                UIPasteboard.general.string = defaultSystemPrompt
                                #else
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(defaultSystemPrompt, forType: .string)
                                #endif
                            } label: {
                                Label("拷贝", systemImage: "doc.on.doc")
                            }
                        }
                } label: {
                    Label("内置默认提示词", systemImage: "doc.text")
                }
            } footer: {
                Text("Flash 和 Pro 模式共用此默认提示词。创建自定义提示词后将替代默认。")
            }

            promptSection(type: "flash", label: "轻量模式 (Flash)", icon: "bolt", color: .blue, prompts: viewModel.flashPrompts)
            promptSection(type: "pro", label: "深度模式 (Pro)", icon: "sparkles", color: .purple, prompts: viewModel.proPrompts)
        }
        .navigationTitle("系统提示词")
        .task { await viewModel.load() }
        .refreshable { await viewModel.load() }
        .sheet(isPresented: $showEditor) {
            PromptEditView(
                promptType: editingType,
                existingPrompt: editingPrompt,
                onSave: { type, text in
                    Task {
                        _ = await viewModel.create(promptType: type, systemPrompt: text)
                        showEditor = false
                    }
                }
            )
        }
        .alert("错误", isPresented: .init(
            get: { viewModel.error != nil },
            set: { if !$0 { viewModel.error = nil } }
        )) {
            Button("确定") {}
        } message: {
            Text(viewModel.error ?? "")
        }
    }

    private func promptSection(type: String, label: String, icon: String, color: Color, prompts: [UserPrompt]) -> some View {
        Section {
            if let active = prompts.first(where: { $0.status == "active" }) {
                activePromptRow(active, icon: icon, color: color)
            } else {
                HStack {
                    Image(systemName: icon).foregroundColor(color)
                    Text("使用内置默认提示词")
                        .foregroundColor(.secondary)
                }
            }

            ForEach(prompts) { p in
                let isActive = p.status == "active"
                HStack {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(p.systemPrompt)
                            .lineLimit(2)
                            .font(.callout)
                            .foregroundColor(isActive ? .primary : .secondary)
                        Text(p.createdAt.prefix(10))
                            .font(.caption2)
                            .foregroundColor(.secondary)
                    }
                    Spacer()
                    if isActive {
                        Text("使用中")
                            .font(.caption)
                            .foregroundColor(color)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 3)
                            .background(color.opacity(0.12))
                            .clipShape(Capsule())
                    }
                }
                .onTapGesture {
                    if !isActive {
                        Task { await viewModel.rollback(p) }
                    }
                }
            }
            .onDelete { offsets in
                let list = prompts
                for i in offsets {
                    Task { await viewModel.delete(list[i]) }
                }
            }

            Button {
                editingType = type
                editingPrompt = nil
                showEditor = true
            } label: {
                Label("新增 \(label) 提示词", systemImage: "plus")
            }
        } header: {
            Label(label, systemImage: icon)
                .foregroundColor(color)
        }
    }

    private func activePromptRow(_ p: UserPrompt, icon: String, color: Color) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Image(systemName: "checkmark.seal.fill")
                    .foregroundColor(color)
                Text("当前生效")
                    .font(.caption)
                    .foregroundColor(color)
                    .fontWeight(.medium)
                Spacer()
                Button("编辑") {
                    editingType = p.promptType
                    editingPrompt = p
                    showEditor = true
                }
                .font(.caption)
            }
            Text(p.systemPrompt)
                .font(.callout)
                .lineLimit(3)
        }
        .padding(.vertical, 4)
    }
}
