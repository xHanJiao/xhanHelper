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

（2026-10-10，用户拍板：计划在上、子任务在下；展开面板底部贴输入栏、按内容自适应、可滚动）

1. **顺序**：把计划便签从 `InputBar` 的 `topTrailingNote` 槽**移出**，改在
   `ChatView.bottomComposer` 作为独立一行 `planNoteRow` 渲染，插在 `SubagentStatusBar` **之前**。
   - `InputBar.swift`：删除 `TopNote` 泛型参数、`topTrailingNote` 属性/两个 init 入参/两个 body 槽位。
   - `ChatView.swift`：新增 `planNoteRow`（右对齐、宽度封顶 360），`bottomComposer` 内先于子任务条。
2. **展示不全**：`subagentOverlay` 由 `VStack` 改 `ZStack(alignment:.bottom)`（点空白收起层在下、
   面板底部对齐），去掉与面板争空间的贪婪同级；`SubagentDetailPanel` 新增 `maxListHeight`，
   用 `GeometryReader`+`PreferenceKey` 实测列表内容高，`ScrollView` 用
   `.frame(height: listHeight>0 ? min(listHeight,maxListHeight) : maxListHeight)` ——
   少则整卡自适应完整展示，多则封顶内部滚动。

## 验证

- `swiftc -typecheck`（macOS 26.3 + iOS 26.0，带 `-default-isolation MainActor`）0 error。
- 真机（用户 Xcode 确认）：计划便签在子任务条上方；展开子任务面板贴在输入栏上方、
  子任务少时整卡完整显示、多时内部滚动。

## 踩坑提示

`ScrollView` 是贪婪视图：放进整高容器（`GeometryReader`）且与另一贪婪视图（`Color.clear`）同级时，
不会贴内容，会被撑满。要「贴内容自适应 + 超限滚动」，必须实测内容高再 `.frame(height:)` 钉住
（对照 `DiffPreviewView` 之所以贴内容，是因为它外面是包内容的容器，而非整高容器）。
