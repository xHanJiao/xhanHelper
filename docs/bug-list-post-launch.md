# 上线后 Bug 清单

> 创建日期: 2026-05-03

---

## 1. 滚动条异常跳动 (P1)

**症状**: 发送消息后滚动条跳到特别靠下的位置，对话框一片空白，需要手动上拉

**根因分析**:
- `ChatView.swift` 中有 3 个 `onChange` 滚动触发器
- `onChange(of: messages.count)` 使用 `.bottom` anchor，当 tool result 消息插入时会跳到该消息底部
- `updateLastAssistantMessage` 通过 remove+append 更新流式内容，可能同时触发多个 onChange
- 多个 onChange 竞争导致滚动位置不可预测

**修复方向**: 统一滚动策略，只在用户主动发送新消息时滚到底部，流式更新时仅确保最新内容可见

**实际修复** (`ChatView.swift:73`):
```swift
// 改前
if let last = viewModel.messages.last {
    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
}
// 改后：跳过流式 placeholder，避免 .bottom 与 .top 两个 anchor 拉扯
if let last = viewModel.messages.last, last.id != "__streaming__" {
    withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
}
```
流式期间只有 `.top` anchor 工作（保持文本顶部可见），不再有 `.bottom` 拉扯。

---

## 2. 输入框不支持长文本 (P2 - 体验优化)

**症状**: 输入长内容时输入框不会自动扩展，也没有展开按钮

**根因分析**:
- `InputBar.swift` 使用 `TextField` 而非 `TextEditor`
- 没有设置 `axis: .vertical` 和 `lineLimit`
- 没有展开/收起机制

**修复方向**: iOS 16+ 的 `TextField` 支持 `axis: .vertical` + `lineLimit(...)`，或添加展开按钮弹出 sheet

**实际修复** (`InputBar.swift`):
- 添加 `@State private var isExpanded = false` 控制展开/收起
- 添加 `showExpandButton` + `@FocusState`：动画期间隐藏按钮消除延后感
- `TextField(axis: .vertical)` + `lineLimit(isExpanded ? 20 : 1)` + `frame(minHeight: isExpanded ? 240 : 0)`
- 展开按钮用 `.overlay(alignment: isExpanded ? .topTrailing : .trailing)` 叠在输入框内右侧，收起时垂直居中，展开时停在右上角
- 点击后立即 `isExpanded.toggle()`，输入框立刻跳到 240pt（约半屏），不是等输入内容撑开

---

## 3. 计划弹窗延迟消失 (P2 - 体验优化)

**症状**: 任务完成后弹窗停一下才消失，不够干脆

**根因分析**:
- `ChatViewModel.swift` 的 `.done` 事件处理中有:
  ```swift
  DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
      isPlanModalVisible = false
      planNodes = []
  }
  ```
- 3 秒延迟是为了让用户看到最终状态（全部绿色勾），但感觉拖沓

**修复方向**: 所有 plan_node_done 事件都到达后（而非固定 3 秒），立即触发渐隐动画

**实际修复**:
- `ChatViewModel.swift` (`done` 事件): 3 秒 `asyncAfter` 改为 `withAnimation(.easeOut(duration: 0.3))` 立即渐隐，0.3 秒后清空 `planNodes`
- `ChatView.swift`: 移除 `ToolCallCard` 渲染（橙色边框卡片）及关联的 `onChange(of: currentToolCall?.function)` 滚动触发器

---

## 4. 后台运行网络断连 (P1)

**症状**: 发消息后切屏到其它 app，回来报网络相关错误

**根因分析**:
- iOS 在 app 进入后台后会挂起网络连接
- SSE 长连接在后台被系统中断
- 没有后台任务保护或断线重连机制

**修复方向**:
- 使用 `beginBackgroundTask` 保护正在进行的请求
- 监听 `scenePhase` 变化，进入后台时做好状态保护
- 添加 SSE 断线自动重连逻辑

**实际修复** (`ChatViewModel.swift`):
- `#if os(iOS)` 引入 `UIKit`，添加 `backgroundTaskID` 属性
- `sendMessage()` 后调用 `beginBackgroundTask()` → 向 iOS 申请约 30 秒后台时间
- `finishStreaming()` 统一入口 → `isStreaming = false` + `endBackgroundTask()`
- done/error/stop/onError 四个出口全部走 `finishStreaming()`
- SSE `onError` 中：`NSURLErrorNetworkConnectionLost` 和 `NSURLErrorCancelled` 静默结束不弹红条
- 后台任务到期 handler 自动断开 SSE 防止 app 被系统 kill

---

## 5. 绕过向量直接访问 memories 库 (P1)

**症状**: 日志显示仍有代码路径不经过向量检索，直接查 memories 表

**根因分析**:
- `LLMMemoryStore.GetRelevant` (memory.go:34-78) 直接调用 `memRepo.ListByUser` 获取所有记忆
- 然后用 LLM 挑选相关记忆（而非用向量相似度）, 完全绕过向量检索
- 调用链: `GetRelevant` → `memRepo.ListByUser` → DB

**修复方向**: 移除 `GetRelevant` 的 LLM 挑选逻辑，改为调用 `vectorStore.Search` + `memRepo.GetByIDs`

---

## 6. 向量检索可能召回当前会话内容 (P0 - 重量级)

**症状**: 检索到的"相关历史对话"可能就是当前对话本身，导致相同内容在上下文中重复

**根因分析**:
- `searchHistoryMessages` (orchestrator.go:426-448) 没有过滤当前 conversation
- 用户消息的嵌入在 `saveMsgEmbedding` 中被异步写入向量库
- 如果写入速度够快，当前对话的消息可能在本轮查找中被检索到
- 返回的上下文会包含与 LLM 已有消息列表重复的内容

**修复方向**:
- 在 `searchHistoryMessages` 中排除当前 conversation ID
- 同时确保 `saveMsgEmbedding` 在检索完成**之后**再写入（调整时序）
- 或在搜索结果中过滤掉当前会话的消息

---

## 修复顺序

| 序号 | 问题 | 严重度 | 状态 |
|------|------|--------|------|
| 1 | 滚动条异常跳动 | P1 | ✅ 已修复 |
| 2 | 输入框不支持长文本 | P2 | ✅ 已修复 |
| 3 | 计划弹窗延迟消失 | P2 | ✅ 已修复 |
| 4 | 后台运行网络断连 | P1 | ✅ 已修复 |
| 5 | 绕过向量直接查 DB | P1 | ✅ 已修复 |
| 6 | 向量检索召回当前会话 | P0 | 待修复 |
