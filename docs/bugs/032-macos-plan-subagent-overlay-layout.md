# 032 · macOS：计划便签与子任务条堆叠顺序错乱 + 子任务展开面板展示不全

## 问题表现

macOS 聊天，当「执行计划便签」与「子任务活动」同时出现时：

1. **子任务条压在计划便签之上**——纵向顺序成了「子任务条 → 计划便签 → 输入胶囊」，
   两个右对齐/全宽不一的浮层叠成台阶，观感突兀。
2. **点子任务条展开后，详情面板展示不全**——面板被撑成一大块、内容贴在顶部，
   下方一片空材质；展开某行正文时更像被截断。

## 根因

两者都源自「底部浮层栈」（`ChatView.bottomComposer`，由 `.safeAreaInset(edge:.bottom)` 承载）：

- **顺序**：`bottomComposer` 的自上而下顺序是 `AskUserBar → SubagentStatusBar → L3 → inputBar`，
  而**计划便签是注入在 `inputBar` 内部最上缘**（旧 `InputBar.topTrailingNote` 槽）。
  于是子任务条天生排在计划便签之前。
- **展示不全**：`subagentOverlay` 把 `SubagentDetailPanel` 放进**整高 `GeometryReader`** 里的
  `VStack`，与同样贪婪的 `Color.clear` 同级；面板内部又是 `ScrollView`（贪婪）。
  三者争抢空间 → 面板不再按内容自适应，被撑到接近 `0.78×高` 的大块，
  内容顶对齐、剩余空材质；再叠加内层滚动，视觉上即「截断/展示不全」。

## 最终方案

（2026-10-10，用户拍板：计划在上、子任务在下；展开面板**内联进底部栈**，夹在计划便签与输入栏之间）

1. **顺序**：把计划便签从 `InputBar` 的 `topTrailingNote` 槽**移出**，改在
   `ChatView.bottomComposer` 作为独立一行 `planNoteRow` 渲染，插在子任务槽 **之前**。
   - `InputBar.swift`：删除 `TopNote` 泛型参数、`topTrailingNote` 属性/两个 init 入参/两个 body 槽位。
   - `ChatView.swift`：新增 `planNoteRow`（右对齐、宽度封顶 360），`bottomComposer` 内先于子任务槽。
2. **展开态改内联**（不再是覆盖式浮层）：删掉 `subagentOverlay` 与 `content` 里的
   `.padding(.bottom, composerHeight)`；`bottomComposer` 的子任务槽二选一——`subagentPanelExpanded`
   时内联渲染 `SubagentDetailPanel`，否则渲染 `SubagentStatusBar`。面板天然夹在计划便签（上）与
   输入栏（下）之间，底部贴输入栏上缘、顶部贴计划便签下缘。
   - 自适应高度：内联在包内容的 `VStack` 里，`ScrollView` 在 ideal 阶段返回内容高 → 面板自动贴内容；
     外部 `.frame(maxHeight: contentHeight * 0.6)` 封顶，超出则内部滚动。
   - `SubagentPanel.swift`：`SubagentDetailPanel` 去掉 `maxListHeight` + `PreferenceKey` 实测那套，
     改回普通 `ScrollView`。
   - `ChatView.swift`：新增 `@State contentHeight`（`onGeometryChange` 量中栏整高）与
     `subagentPanelMaxHeight`。

## 验证

- `swiftc -typecheck`（macOS 26.3 + iOS 26.0，带 `-default-isolation MainActor`）0 error。
- `SubagentPanel.swift` 的 `#Preview("子任务面板 · 内联")`：Xcode 画布复核展开态位置与自适应高度。
- 真机（用户 Xcode 确认）：计划便签在子任务面板上方；展开面板贴在输入栏上方、
  子任务少时整卡完整显示、多时内部滚动。

## 踩坑提示

- **`ScrollView` 是贪婪视图**：放进整高容器（`GeometryReader`）且与另一贪婪视图同级时不会贴内容，
  会被撑满；只有放进**包内容的容器**（如 `safeAreaInset` 里的 `VStack`）时，ideal 阶段才会返回
  内容高、自动自适应。这就是"覆盖式浮层"出问题的根源，也是改回"内联"的原因。
- 覆盖式浮层是**整栏高**容器，所以面板一旦放进去就失去自适应；要"底部贴输入栏"必须内联。
- 内联后没有遮罩层，只能靠面板头部「收起」收回（不再支持点空白收起）。
