# 对话里的文档卡片刷新即消失 / 改文档时卡片飞到上一条消息

## 问题表现

macOS 客户端聊天里的文档卡片（`DocumentRefCard`）：

1. **刷新后消失**：新建会话里 agent 建/改的文档，卡片当场显示，刷新（切走再回来、重进会话）后就没了。
2. **改文档时卡片「上下飞」**：已有会话里 agent 改一篇文档，卡片最终落到**上一条回复**的位置，而不是当前这条。

用户原话：「对话里的文档卡片不持久化，刷新回来就没了，之前似乎修过，但是不起效」「已有的会话，再改文档的时候，文档卡片会上下飞（虽然是当前消息改的文档，但是卡片去上一个消息了）」。

## 根因（两个，叠加）

本地产物管线（`docs/design/local-artifacts/design.md`）把卡片按会话落盘到
`~/Library/Application Support/<root>/local_results/<convId>.json`，重载时按
`[first.createdAt, last.createdAt]` 读回、按时间排序、再「归位」。

### 根因 A：新会话第一轮落盘时用了空会话 id

卡片在 SSE `tool_result` 时生成（`ChatViewModel.swift` 的 `.toolResult` 分支），此时新会话的
`conversationId` 还是 `""`（真实 id 要等 `done` 事件）。`insertLocalArtifact` 用它落盘：

```
conversationId: conversationId ?? ""   →  写到 local_results/.json（空文件名）
```

刷新时 `mergeLocalToolResults` 按真实 id 读 `<convId>.json` → 读不到 → 卡片丢。
**实锤**：`~/Library/Application Support/xhanHelper/local_results/.json`（413B，内含一条 chart）。

### 根因 B：时间戳按字符串比较，本地 `Z` vs 服务端 `+08:00`

- 卡片 `createdAt` = `ISO8601DateFormatter().string(from: Date())` → UTC，如 `2026-09-26T04:04:32Z`。
- 服务端（本地 SQLite 后端）`created_at` 带东八区，如 `2026-09-26T12:04:12.879551+08:00`（同一时刻）。

两处都拿**字符串**比：

- `LocalToolResultStore.entries` 的范围过滤 `createdAt >= from && <= to`：当天 08:00 后的窗口里
  `from` 以 `12:` 开头、卡片以 `04:` 开头 → 卡片 < from → **被过滤掉（刷新即消失）**。
- `ChatViewModel.mergeLocalToolResults` 的 `sort { $0.createdAt < $1.createdAt }`：卡片被排到整轮之前，
  随后 `LocalArtifactMerge.reposition` 的「最近 assistant」把它吸到**上一条回复**（卡片「飞」过去）。

> 云端 MySQL（DSN 默认 `loc=UTC`）返回 `Z`，本地 SQLite 后端返回 `+08:00`，所以本地会话中招最重。
> 设计文档 §九只把它记成「精度不同」，实际差的是**时区偏移 8 小时**。

### 附：归位算法本身的脆弱点

`reposition` 原来锚定「最近的 assistant」。若某轮 assistant 恰好无可见正文（被服务端
`cleanMessages` 过滤），该轮没有 assistant 锚点，卡片就会被吸到上一轮——即使时间排序正确也会错位。

## 最终方案

1. **按解析后的 `Date` 比较时间**（core 修复，同时治 A 之外的两个症状）
   - 新增 `LocalArtifacts/LocalArtifactTime.swift`：`parse`（兼容带/不带小数秒、`Z`/`+08:00`）、
     `isOrdered`、`isWithin`；解析失败回退字符串比较。
   - `LocalToolResultStore.entries` 与 `ChatViewModel.mergeLocalToolResults` 改用上述函数。
2. **新会话第一轮补落盘**
   - `insertLocalArtifact`：`conversationId` 为空时**不落盘**（仅留内存）。
   - `done` 里由空会话转成真实 id 时，`persistLocalArtifactsUnderConversation(cid)` 遍历内存中
     `localToolResult != nil` 的消息补 `upsert`，并 `removeConversation("")` 清掉历史遗留空键文件。
3. **归位锚定「前一条 user 消息」**
   - `LocalArtifactMerge.reposition` 增加 `isUser` 谓词：产物插到其所属轮的 user 之后；无 user（隐藏轮）
     才回退「最近 assistant」。现有 9 条重定位单测语义全部不变。

## 验证

- `cd client/xHelper && swift test` 全绿（含新增：`testRangeFilterHandlesTimezoneOffsetMismatch`、
  `testLocalStaysInItsRoundWhenItsAssistantMissing`、`testLocalInItsRoundBetweenUserAndAssistant`、
  `testRemoveConversationWipesDiskAndCache`）。
- macOS / iOS `swiftc -typecheck` 0 error。
- 真机：新建会话建文档 → 刷新卡片在；已有本地会话改文档 → 刷新后卡片仍在**当前轮**上方；
  `local_results/` 下不再新增 `.json` 空文件。

## 涉及文件

- `client/xHelper/xHelper/Shared/LocalArtifacts/LocalArtifactTime.swift`（新增）
- `client/xHelper/xHelper/Shared/LocalArtifacts/LocalArtifactMerge.swift`
- `client/xHelper/xHelper/Shared/Services/LocalToolResultStore.swift`
- `client/xHelper/xHelper/Shared/ViewModels/ChatViewModel.swift`
- `client/xHelper/Tests/LocalArtifactsCoreTests/{LocalArtifactMergeTests,LocalToolResultStoreTests}.swift`
- `docs/design/local-artifacts/design.md`（§九 更正 + 后续变更）
