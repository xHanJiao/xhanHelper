import MarkdownUI // 第三方 Markdown 渲染库
import SwiftUI

// AgentListView — 智能体列表页
// 功能：展示智能体列表、查看详情、创建/编辑/删除智能体、快速发起对话
struct AgentListView: View {
    // @StateObject: 持有 ViewModel 的生命周期，视图重建时不会重新创建
    @StateObject var viewModel: AgentViewModel

    // @State: 视图内部的临时状态，由当前视图拥有
    // selectedAgent: 用户点击列表项时赋值 → 触发 detail sheet
    @State private var selectedAgent: Agent?

    // editName/editPrompt: 编辑表单的输入缓冲
    // 为什么用 @State 而非直接绑 Agent 属性？
    // → 编辑取消时需要丢弃修改，@State 作为中间缓冲，保存时才提交
    @State private var editName = ""
    @State private var editPrompt = ""
    @State private var editModel = "deepseek-chat"
    @State private var editThinking = true
    @State private var editTemperature = ChatViewModel.cachedTemperature
    @State private var editMaxTokens = ChatViewModel.cachedMaxTokens

    // createName/createPrompt: 创建表单的输入缓冲
    @State private var createName = ""
    @State private var createPrompt = ""
    @State private var createModel = "deepseek-chat"
    @State private var createThinking = true
    @State private var createTemperature = ChatViewModel.cachedTemperature
    @State private var createMaxTokens = ChatViewModel.cachedMaxTokens

    // body: 声明式 UI，每次 @Published 变化时会重新计算
    var body: some View {
        // List: 平台适配的列表容器（iOS 上即 UITableView）
        List {
            // 空状态：没有智能体且不在加载中
            if viewModel.agents.isEmpty && !viewModel.isLoading {
                Text("还没有智能体，点击右上角 + 创建")
                    .font(.subheadline)       // 次级标题字号
                    .foregroundColor(.secondary) // 系统灰色
            } else {
                // ForEach: 根据 Identifiable 数据生成视图数组
                // agent.id 作为 SwiftUI 的 diff 依据（需要 Agent 实现 Identifiable）
                ForEach(viewModel.agents) { agent in
                    // HStack: 水平排列子视图
                    HStack {
                        // VStack: 垂直排列子视图
                        // alignment: .leading → 左对齐
                        // spacing: 4 → 子视图间距4pt
                        VStack(alignment: .leading, spacing: 4) {
                            Text(agent.name)
                                .font(.subheadline.weight(.medium)) // 次级标题 + 中等粗细
                        }
                        // Spacer: 弹性空间，把聊天按钮推到右侧
                        Spacer()
                        // 快速聊天按钮：点击直接以该智能体发起对话
                        Button {
                            viewModel.chatWithAgent(agent)
                        } label: {
                            Image(systemName: "bubble.left.and.bubble.right") // SF Symbol 聊天气泡
                                .font(.body)
                                .foregroundColor(.blue)
                        }
                        .buttonStyle(.plain) // 去掉按钮默认样式（不加底色边框）
                    }
                    .padding(.vertical, 4) // 行上下内边距
                    // contentShape: 定义点击热区为整行矩形
                    // 不加的话 SwipeActions 和 TapGesture 可能冲突
                    .contentShape(Rectangle())
                    // onTapGesture: 点击整行 → 打开详情 sheet
                    .onTapGesture { selectedAgent = agent }
                    // swipeActions: 右滑露出操作按钮
                    // edge: .trailing → 从右侧滑动（中文习惯"右滑删除"）
                    .swipeActions(edge: .trailing) {
                        // role: .destructive → 系统自动染红色
                        Button(role: .destructive) {
                            // Task: 在 SwiftUI 中启动异步任务
                            Task { await viewModel.delete(id: agent.id) }
                        } label: {
                            Label("删除", systemImage: "trash")
                        }
                    }
                    // edge: .leading → 从左侧滑动
                    .swipeActions(edge: .leading) {
                        Button {
                            // 点击编辑 → 把当前智能体信息拷入编辑缓冲
                            viewModel.editingAgent = agent
                            editName = agent.name
                            editPrompt = agent.systemPrompt
                            editModel = agent.model.isEmpty ? "deepseek-chat" : agent.model
                            editThinking = true
                            editTemperature = ChatViewModel.cachedTemperature
                            editMaxTokens = ChatViewModel.cachedMaxTokens
                        } label: {
                            Label("编辑", systemImage: "pencil")
                        }
                        .tint(.orange) // 编辑按钮用橙色（区别于红色的删除）
                    }
                }
            }
        }
        // 导航栏标题
        .navigationTitle("智能体")
        // 点击空白区域收起键盘（项目中自定义的 ViewModifier）
        .dismissKeyboardOnTap()
        // toolbar: 导航栏右侧按钮
        .toolbar {
            // 右上角 + 号 → 打开创建表单
            Button { viewModel.showCreateSheet = true } label: {
                Image(systemName: "plus")
            }
        }
        // task: 视图出现时触发，await 异步加载数据
        // 等价于 UIKit 的 viewWillAppear + async
        .task { await viewModel.load() }
        // refreshable: 下拉刷新
        .refreshable { await viewModel.load() }
        // onChange: 监听 showCreateSheet 变化
        // { _, new in ... } → 第一个参数是旧值（用 _ 忽略），new 是新值
        // 当创建表单关闭时 (!new) → 清空输入缓冲
        .onChange(of: viewModel.showCreateSheet) { _, new in
            if !new {
                createName = ""; createPrompt = ""; createModel = "deepseek-chat"
                createThinking = true
                createTemperature = ChatViewModel.cachedTemperature
                createMaxTokens = ChatViewModel.cachedMaxTokens
            }
        }
        // onChange: 监听 editingAgent 的 id 变化
        // 当编辑表单关闭时 (editingAgent==nil) → 清空编辑缓冲
        .onChange(of: viewModel.editingAgent?.id) { _, _ in
            if viewModel.editingAgent == nil {
                editName = ""; editPrompt = ""; editModel = "deepseek-chat"
                editThinking = true
                editTemperature = ChatViewModel.cachedTemperature
                editMaxTokens = ChatViewModel.cachedMaxTokens
            }
        }
        // sheet(item:): 根据可选值自动弹出/关闭 sheet
        // $selectedAgent 非 nil 时弹出，dismiss 时系统自动置 nil
        .sheet(item: $selectedAgent) { agent in
            // NavigationStack: SwiftUI 的导航栈（iOS 16+）
            NavigationStack {
                // ScrollView: 可滚动容器，内容超出屏幕时可上下滑动
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        Text(agent.name)
                            .font(.title2.weight(.semibold)) // 二级标题 + 半粗体
                        // Markdown: MarkdownUI 库的渲染组件
                        // 把 systemPrompt（Markdown 格式字符串）渲染为富文本
                        Markdown(agent.systemPrompt)
                            .markdownTheme(.gitHub)  // 使用 GitHub 风格主题
                            .frame(maxWidth: .infinity, alignment: .leading) // 撑满宽度 + 左对齐
                            .markdownCodeSyntaxHighlighter(.plainText) // 代码块用纯文本高亮（不加语法着色）
                    }
                    .padding()
                }
                .navigationTitle("智能体详情")
                .navigationBarTitleDisplayMode(.inline) // 导航标题用内联样式（不放大）
                .toolbar {
                    // ToolbarItem: 导航栏上的按钮
                    ToolbarItem {
                        Button("编辑") {
                            // 先关闭详情 sheet（置 nil）
                            selectedAgent = nil
                            // 再打开编辑 sheet（赋值 editingAgent）
                            viewModel.editingAgent = agent
                            editName = agent.name
                            editPrompt = agent.systemPrompt
                            editModel = agent.model.isEmpty ? "deepseek-chat" : agent.model
                            editThinking = true
                            editTemperature = ChatViewModel.cachedTemperature
                            editMaxTokens = ChatViewModel.cachedMaxTokens
                        }
                    }
                }
            }
        }
        // sheet(isPresented:): 基于 Bool 值的 sheet 控制
        .sheet(isPresented: $viewModel.showCreateSheet) {
            // 复用 agentForm，传入创建专用的 Binding 和回调
            agentForm(title: "创建智能体", name: $createName, prompt: $createPrompt, model: $createModel, thinking: $createThinking, temperature: $createTemperature, maxTokens: $createMaxTokens,
                onDismiss: { viewModel.showCreateSheet = false },
                onSubmit: { await viewModel.create(name: createName, systemPrompt: createPrompt, model: createModel == "deepseek-chat" ? nil : createModel, thinking: createThinking, temperature: createTemperature, maxTokens: createMaxTokens) }
            )
        }
        // sheet(item:): 编辑表单 — editingAgent 非 nil 时弹出
        .sheet(item: $viewModel.editingAgent) { agent in
            // 复用同一个 agentForm，传入编辑专用的 Binding 和回调
            agentForm(title: "编辑智能体", name: $editName, prompt: $editPrompt, model: $editModel, thinking: $editThinking, temperature: $editTemperature, maxTokens: $editMaxTokens,
                onDismiss: { viewModel.editingAgent = nil },
                onSubmit: { await viewModel.update(agent: agent, name: editName, systemPrompt: editPrompt, model: editModel == "deepseek-chat" ? nil : editModel, thinking: editThinking, temperature: editTemperature, maxTokens: editMaxTokens) }
            )
        }
    }
}

// agentForm: 创建/编辑共用的表单组件
// 用 private func 而非 struct 的原因：轻量，不需要独立生命周期
// 参数说明：
//   - title: 导航标题（"创建智能体" / "编辑智能体"）
//   - name/prompt: Binding → 父视图的状态引用，双向绑定
//   - onDismiss: 取消回调（关闭 sheet）
//   - onSubmit: 保存回调（async 异步）
private func agentForm(title: String, name: Binding<String>, prompt: Binding<String>, model: Binding<String>, thinking: Binding<Bool>, temperature: Binding<Double>, maxTokens: Binding<Int>, onDismiss: @escaping () -> Void, onSubmit: @escaping () async -> Void) -> some View {
    NavigationStack {
        // Form: 系统样式的分组表单（iOS 上为 grouped UITableView）
        Form {
            // Section: 表单分组，带 header/footer 文字
            Section("名称") {
                TextField("智能体名称", text: name)
            }
            Section("模型") {
                Picker("模型", selection: model) {
                    Text("DeepSeek Chat").tag("deepseek-chat")
                    Text("DeepSeek Reasoner").tag("deepseek-reasoner")
                    Text("豆包").tag("doubao")
                }
                Toggle("Thinking", isOn: thinking)
            }
            Section("参数") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Temperature: \(String(format: "%.1f", temperature.wrappedValue))")
                        .font(.subheadline)
                    Slider(value: temperature, in: 0...2, step: 0.1)
                }
                VStack(alignment: .leading, spacing: 8) {
                    Text("Max Tokens: \(maxTokens.wrappedValue)")
                        .font(.subheadline)
                    Slider(value: Binding(get: { Double(maxTokens.wrappedValue) }, set: { maxTokens.wrappedValue = Int($0) }), in: 100...32000, step: 100)
                }
            }
            Section("智能体设定") {
                TextEditor(text: prompt)
                    .frame(minHeight: 180)
                    .scrollContentBackground(.hidden)
                Text("\(prompt.wrappedValue.count)/3000")
                    .font(.caption)
                    .foregroundColor(prompt.wrappedValue.count > 3000 ? .red : .secondary)
            }
        }
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            // placement: .cancellationAction → iOS 自动放在左侧
            ToolbarItem(placement: .cancellationAction) {
                Button("取消") { onDismiss() }
            }
            // placement: .confirmationAction → iOS 自动放在右侧，加粗
            ToolbarItem(placement: .confirmationAction) {
                Button("保存") {
                    // Task: 在同步上下文中启动 async 闭包
                    Task { await onSubmit() }
                }
                // disabled: 条件满足时按钮灰色不可点
                // trimmingCharacters(.whitespaces): 去除首尾空格后检查是否为空
                // ||: 名称空 或 提示词空 或 超过3000字 → 禁用保存
                .disabled(name.wrappedValue.trimmingCharacters(in: .whitespaces).isEmpty
                    || prompt.wrappedValue.trimmingCharacters(in: .whitespaces).isEmpty
                    || prompt.wrappedValue.count > 3000)
            }
        }
    }
}
