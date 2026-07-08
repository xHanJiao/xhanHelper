# 消息列表上滑加载：滚动位置保持（反转列表 / 最终方案 D2）

## 背景

`MessageListView` 每次只拉取最新若干条消息放入 `ChatViewModel.messages` 并渲染。上滑到顶加载"更早消息"时（`messages = newMsgs + messages`），期望：**当前可见的消息保持不动，新消息静默填充到其上方**，用户可继续上滑。

实际问题：加载后视图跳到"新拉取那批里最老的一条"，需要重新往下滚，体验很差；快速甩动时尤其必现，越往深处越明显。

## 排障历程与关键结论

按时间顺序尝试了多种"prepend 后把视图滚回原位"的做法，均在快甩场景失败，逐步定位到根因：

1. `scrollPosition(id:anchor:)` 双向绑定 → prepend 后被框架清成 `nil`，`defaultScrollAnchor` 接管跳走。
2. 改 `ScrollViewReader.proxy.scrollTo(id, anchor:.top)` → 慢拉正常，快甩仍失败。
3. 加诊断日志（`onScrollPhaseChange` / `onScrollGeometryChange` + 时间戳），复现后确认：
   - 触发/复原发生在惯性滑行（`decelerating`）期间时，`scrollTo` 被无视。
   - 证据：同批加载，`interacting`（手指在屏）时 `scrollTo` 后 `offsetY=7843`（成功）；`decelerating`（纯惯性）时 `offsetY=-62`（停最顶端，失败）。
4. 换 iOS 18 `ScrollPosition.scrollTo(y:)` 做 offset 增量补偿 → **同样被惯性覆盖**（三次加载 `COMPENSATE` 后 `offsetY` 均停在 -62/-127，目标 7540/4817/3288 全部落空）。

**最终结论：SwiftUI `ScrollView` 在 deceleration（惯性）进行中，`scrollTo`（任何 API）基本是 no-op。** 因此"先 prepend 再 scrollTo 拉回"这条路在快甩下注定失败。

排除项：
- **分页 id 重复**：后端游标区间 `[oldestTime, cursorTime)` 边界干净，日志 `dupCount=0`。
- **布局时序**：`contentHeight` 在 `scrollTo` 前已算好，anchor 行已完成布局。

## 采用方案：反转列表（inverted list）

不再与惯性对抗，而是从结构上绕开它。

### 原理

- 整个 `ScrollView` 上下翻转（`.scaleEffect(y: -1)`），数据倒序显示，每行再翻转一次转正。
- 视觉底部 = 最新消息，视觉顶部 = 最旧消息。
- 用户上滑看历史 = 在翻转坐标系里往**内容末尾**滚动。
- `loadMore` 的 `messages = newMsgs + messages`（旧消息进数组最前）→ 倒序后落到翻转坐标的**末尾** → **往末尾追加不改变当前可视区 offset**，内容纹丝不动，**与惯性无关**。

这是聊天类 App 的行业标准做法（微信/iMessage 上翻聊天记录的体验）。

### `MessageListView.swift` 要点

```
ScrollView {
    VStack(spacing: 12) {
        if status { StatusRow().scaleEffect(y: -1) }          // 逻辑最前→视觉最底
        ForEach(messages.reversed()) { messageRow($0).scaleEffect(y: -1) }
        loadMoreHeader.scaleEffect(y: -1)                     // 逻辑最后→视觉最顶
    }
    .padding(.vertical)
}
.scaleEffect(y: -1)
.defaultScrollAnchor(.top)   // 翻转坐标 top = 视觉底 = 最新消息（初次定位到最新）
```

- **每行翻转**：容器与每行都 `.scaleEffect(y: -1)`，两次翻转抵消，行内文字正常。
- **行内顺序修正**：流式那条把 `ChatBubble` 写在前、`ThinkingBubble` 写在后；经外层翻转后恢复「思考在上、回复在下」。
- **`onScrollVisibilityChange`** 挂在 header 上；header 在布局末尾（翻转后视觉顶部），用户上滑到视觉顶部即触发 `loadMore`。
- 移除上一轮全部补偿代码：`scrollPos`/`.scrollPosition`/`.onScrollGeometryChange` 补偿/`pendingRestore`/`preRestoreHeight`/`scrollTo`。

### `ChatViewModel.swift`

- `loadMore` 的 `messages = newMsgs + messages` **保持不变**（反转方案下天然正确）。
- 暂留 `loadMore` 的 `START/FETCHED` 两条日志用于验证；稳定后清理。

## 回归验证点（反转列表已知坑）

1. **流式贴底**：发消息后 `userMsg`+`streamingAssistant` 追加，是否仍自动停在视觉底部。
2. **首次进入**定位到最新消息（视觉底部）。
3. **行内交互**：`scaleEffect(y:-1)` 下 ChatBubble 长按复制/链接点击/文本选择是否正常。
4. **流式那条**思考气泡与回复上下顺序正确。
5. **消息极少/不足一屏**时的表现。
6. **上滑加载**：快甩、慢拉、连续猛甩、深处加载——内容都应纹丝不动、无回弹、无急停。

## 可选优化（暂未采用）

- **LazyVStack**：反转列表标配，消息多时性能更好；翻转下 lazy 动态高度需多测。当前先用 `VStack`（改动小、风险低），性能有问题再换。

## 回滚

改动集中在 `MessageListView.swift`。checkpoint 提交（commit `1b2914b`）为 `ScrollViewReader` + 日志版，可一键回退。
