import SwiftUI

struct PromptEditView: View {
    let promptType: String
    let existingPrompt: UserPrompt?
    let onSave: (String, String) -> Void

    @State private var systemPrompt: String
    @Environment(\.dismiss) private var dismiss

    init(promptType: String, existingPrompt: UserPrompt?, onSave: @escaping (String, String) -> Void) {
        self.promptType = promptType
        self.existingPrompt = existingPrompt
        self.onSave = onSave
        _systemPrompt = State(initialValue: existingPrompt?.systemPrompt ?? "")
    }

    private var typeLabel: String {
        promptType == "flash" ? "轻量模式 (Flash)" : "深度模式 (Pro)"
    }

    private var typeIcon: String {
        promptType == "flash" ? "bolt" : "sparkles"
    }

    private var typeColor: Color {
        promptType == "flash" ? .blue : .purple
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    HStack {
                        Image(systemName: typeIcon)
                            .foregroundColor(typeColor)
                        Text(typeLabel)
                        Spacer()
                        Text("新建")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }

                    TextEditor(text: $systemPrompt)
                        .font(.callout)
                        .frame(minHeight: 200)
                } header: {
                    Text("提示词类型")
                } footer: {
                    Text("此提示词将替代默认系统提示词。")
                }

                Section {
                    Button {
                        guard !systemPrompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
                        onSave(promptType, systemPrompt)
                    } label: {
                        Label("保存", systemImage: "checkmark")
                    }
                    .disabled(systemPrompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .navigationTitle(existingPrompt != nil ? "编辑提示词" : "新增提示词")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }
                }
            }
        }
    }
}
