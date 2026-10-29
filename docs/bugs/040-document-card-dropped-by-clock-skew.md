# 文档卡片偶发消失：客户端时钟比服务端快，被加载窗口的上界剔掉

## 问题表现

macOS 聊天里的文档卡片（`DocumentRefCard`）平时正常出现、切会话/刷新也在，但**有时候**会消失：

- 某一轮 agent 建/改了文档 → 卡片当场出现；
- 重开会话（或 tab 被 LRU 淘汰后重新加载）→ 这一轮的卡片**不见了**；
- **再发一条消息** → 卡片自己又回来。

同一现象也会发生在「手动停止 / 出错」收尾的那一轮（服务端没有落 assistant 消息时）。

## 根因

本地产物加载路径（`ChatViewModel.mergeLocalToolResults`）把卡片的时间戳和**服务端**消息的时间戳放进同一个
区间过滤：

```
窗口 = [页首消息 createdAt, 页尾消息 createdAt]   // 服务端时钟
卡片 createdAt 落在窗口外 → 直接丢弃               // 客户端时钟
```

两个时间来自**不同时钟域**：

- 卡片：`insertLocalArtifact` 里 `ISO8601DateFormatter().string(from: Date())` → 本机时钟；
- 消息：服务端落库时间 → 服务器时钟。

实测这台 Mac 的时钟（当时）**比服务器快 ~44 秒**：

```
helper.xhanx.cn  Date: 02:17:37 GMT
baidu.com        Date: 02:18:05 GMT   # 两路互证：确实是本机快
本机              date  → 02:18:49
```

于是卡片时间天生比同一轮 assistant 消息晚最多 ~44s：

- 卡片产生后这一轮还要跑 ≥44s → 页尾时间被推过卡片时间 → 卡片保住；
- 卡片在结尾附近产生、回复很快结束（很常见）→ `assistant.createdAt` 仍早于卡片 → **被上界剔掉**；
- 下一轮再发消息，页尾时间变大 → 卡片重新落回窗口 → **自己又回来**。这就是「有时候才消失」。

停止/出错轮是同一处的另一条触发路径：那一轮没有落 assistant 消息，页尾就是本轮 user 消息（更早），
卡片同样被上界剔掉 —— 与时钟差无关。

## 最终方案（2026-10-29 用户拍板 A+B）

不再用时间窗口当唯一闸门，归位改按**位置锚点**：

1. **A · 去掉上界**：`LocalToolResultStore.entries(for:from:to:)` → `allEntries(for:)`，读取不过滤；
   下界判定挪到加载方（`LocalArtifactTime.isOnOrAfter`），只判「不早于页首」。
2. **B · 锚点归位**：`Entry` 记 `anchorUserID`（产生这张卡的那一轮 user 消息 id）。
   - 落盘时（`insertLocalArtifact` / `upsertRoundDocumentRef` / `persistLocalArtifactsUnderConversation`）
     写入锚点；白板首轮补落盘改到 done 里 user id 替换**之后**，锚点直接是服务端 id；
   - done 拿到服务端 user id 后 `reattachAnchor(from:to:)` 把本轮卡片的锚点改写过去；
   - 加载时：锚点在本页 → 直接插在该 user 消息之后（**不参与时间排序**）；锚点不在本页 → 跳过（翻页再读）；
     旧数据无锚点 / 停止轮锚点是本地 id → 回退「下界过滤 + 时间排序 + `reposition`」。
3. `LocalArtifactMerge.placeAnchored` 承担锚点插入（纯逻辑，`swift test` 覆盖）。

**不做**：不改系统时钟（那是用户机器的事），不迁移存量条目（无锚点的旧数据走回退分支，照样可见）。

## 验证

- `cd client/xHelper && swift test` 全绿（313 例；新增/改写：无锚点旧 JSON 解码、锚点往返、`reattachAnchor`、
  `allEntries` 不被上界过滤、`placeAnchored` 三条、`isOnOrAfter` 两条）。
- macOS `swiftc -typecheck` 0 error。
- 真机：建/改文档后重开会话卡片在；停止一轮后重开也在；连发两轮卡片不串轮。

## 涉及文件

- `client/xHelper/xHelper/Shared/Services/LocalToolResultStore.swift`
- `client/xHelper/xHelper/Shared/LocalArtifacts/LocalArtifactTime.swift`
- `client/xHelper/xHelper/Shared/LocalArtifacts/LocalArtifactMerge.swift`
- `client/xHelper/xHelper/Shared/ViewModels/ChatViewModel.swift`
- `client/xHelper/Tests/LocalArtifactsCoreTests/{LocalToolResultStoreTests,LocalArtifactMergeTests}.swift`
- `docs/design/local-artifacts/design.md`（后续变更）
