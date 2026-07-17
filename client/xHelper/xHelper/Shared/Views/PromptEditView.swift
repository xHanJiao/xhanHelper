import SwiftUI

// PromptEditView — 系统提示词编辑器
// 用于编辑 Flash（轻量模式）或 Pro（深度模式）的自定义系统提示词
// 用户在 设置 → 提示词 中进入此页面
struct PromptEditView: View {
    // 以下三个是初始化时注入的常量，视图生命周期内不变
    // "flash" 或 "pro"
    let promptType: String
    // existingPrompt: 如果要编辑已有提示词则传入，新建则 nil
    let existingPrompt: UserPrompt?
    // onSave: 保存回调，把 (promptType, systemPrompt) 传回父视图处理
    let onSave: (String, String) -> Void

    // @State: 编辑缓冲，初始化为已有内容或空串
    @State private var systemPrompt: String
    // @Environment(\.dismiss): 读取系统的 dismiss 动作
    // 调用 dismiss() 即可关闭当前 sheet/modal
    @Environment(\.dismiss) private var dismiss

    // init: 手动实现是因为 @State 需要在 init 中设初始值
    // _systemPrompt = State(initialValue: ...) 是 @State 的底层写法
    // 等价于声明时 @State private var systemPrompt: String = "..."
    init(promptType: String, existingPrompt: UserPrompt?, onSave: @escaping (String, String) -> Void) {
        self.promptType = promptType
        self.existingPrompt = existingPrompt
        self.onSave = onSave
        // @State 包装器的 projected value 是 Binding
        // 这里通过 _systemPrompt（State 类型）用 initialValue 初始化
        _systemPrompt = State(initialValue: existingPrompt?.systemPrompt ?? "")
    }

    // 计算属性：根据 promptType 返回不同的 UI 文案
    private var typeLabel: String {
        promptType == "flash" ? "轻量模式 (Flash)" : "深度模式 (Pro)"
    }

    // SF Symbol 图标名: flash → 闪电, pro → 星光
    private var typeIcon: String {
        promptType == "flash" ? "bolt" : "sparkles"
    }

    // 主题色: flash → 蓝, pro → 紫
    private var typeColor: Color {
        promptType == "flash" ? .blue : .purple
    }

    var body: some View {
        // NavigationStack: 提供导航栏容器
        NavigationStack {
            Form {
                // 第一个 Section: 提示词类型展示 + 编辑区
                Section {
                    // 类型信息行: 图标 + 名称 + "新建"标签
                    HStack {
                        Image(systemName: typeIcon)
                            .foregroundColor(typeColor) // 图标染色
                        Text(typeLabel)
                        Spacer() // 把"新建"推到右侧
                        Text("新建")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }

                    // TextEditor: 多行文本编辑器
                    TextEditor(text: $systemPrompt)
                        .font(.callout) // callout 字号（介于 body 和 footnote 之间）
                        .frame(minHeight: 250) // 最小高度，保证编辑区足够大
                        // iOS 16+: 隐藏 TextEditor 自带滚动层
                        // 让 Form 统一处理滚动，避免滚不到底部
                        .scrollContentBackground(.hidden)
                } header: {
                    // Section 头部文字
                    Text("提示词类型")
                } footer: {
                    // Section 底部说明文字（灰色小字）
                    Text("此提示词将替代默认系统提示词。")
                }

                // 第二个 Section: 保存按钮
                Section {
                    Button {
                        // guard: 空内容时直接 return，不触发保存
                        guard !systemPrompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
                        // 调用父视图注入的保存回调
                        onSave(promptType, systemPrompt)
                    } label: {
                        // Label: 图标 + 文字的组合视图
                        Label("保存", systemImage: "checkmark")
                    }
                    // 内容为空时按钮禁用（灰色不可点）
                    .disabled(systemPrompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            // 导航栏标题：根据是否编辑已有内容显示不同文案
            .navigationTitle(existingPrompt != nil ? "编辑提示词" : "新增提示词")
#if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
#endif
            .toolbar {
                // cancellationAction: iOS 自动放在导航栏左侧
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() } // 关闭当前页面
                }
            }
        }
    }
}
