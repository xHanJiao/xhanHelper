# 文档卡片偶发消失：客户端时钟比服务端快，被加载窗口的上界剔掉

## 问题表现

macOS 聊天里的文档卡片（`DocumentRefCard`）平时正常出现、切会话/刷新也在，但**有时候**会消失：

- 某一轮 agent 建/改了文档 → 卡片当场出现；
- 重开会话（或 tab 被 LRU 淘汰后重新加载）→ 这一轮的卡片**不见了**；
- **再发一条消息** → 卡片自己又回来。

同一现象也会发生在「手动停止 / 出错」收尾的那一轮（服务端没有落 assistant 消息时）。

## 人话版：两块表

代码只比了两个时间数字，本质上是在比「卡片几点产生的」和「这条回复几点落库的」——而它们来自**两块不同的表**：

- 消息时间：服务器写入时打的（服务器时钟）；
- 卡片时间：卡片生成时客户端打的（这台 Mac 的时钟）。

加载时为了「只显示当前这一页里的卡片」，写了一个很自然的条件：

> 卡片时间要落在「页首消息时间 ～ 页尾消息时间」之间。

这个条件里埋了三个假设：①两块表是同一块；②卡片产生于本轮 user 消息之后；③卡片产生于本轮 assistant 消息之前。
①不成立时 ②③ 都跟着错，而代码只是比了两个数字，看不出问题。

打个比方：北京的人记「我几点发出的请求」，东京的人记「我几点收到的请求」，然后拿两张纸排先后——
只要有一块表没校准，先后就是随机的，纸面上的数字却毫无破绽。

**为什么「有时候」**：窗口上界是「这一轮什么时候结束」，它随延迟变化——卡片产生后这轮还跑了 44 秒以上就没事，
回复很快结束就掉出窗口；下一轮再发消息、上界变大，卡片又自己回来。「用得越快越容易中招」。

**为什么当场不坏、重开才坏**：当场走内存，卡片插进列表就完了；只有「从盘读回来重新摆位」这条路会过窗口判断。
所以症状永远出现在重开之后——回头看时它已经没了，而当时明明显示过。

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

## 复盘：这类 bug 的形状

**形状**：把「客户端产生的时间」和「服务端产生的时间」放进同一个不等式 / 区间里比。

三个特征，凑齐了就该警觉：

1. **只在部分设备上出现**（取决于对方那块表快慢，不是取决于代码分支）；
2. **会自愈**：一有无关活动（比如再发一条消息、窗口边界变了）就恢复 —— 说明是**查询边界**问题，
   不是数据被删了。消失的东西自己回来，几乎总是"没查出来"，而不是"没了"；
3. **当场正常、重开才坏**：内存路径不过滤、持久化路径过滤 → 一定要走一次"从盘恢复"才复现。

**为什么 dev 测不出**：本地后端与客户端在同一台机器上共用一块表，假设①天然成立。
**环境差异型 bug 在 dev 里是隐形的**——这正是它最阴的地方。

**这一次的教训不是"加容差"**，而是**跨域的东西不要比数值，要比身份**：
卡片落盘时记下"它属于哪一轮"（那一轮 user 消息的 id，一个名字），加载时按名字插回；
名字找不到才退回时间比较，且只判下界（加载的永远是最新一页，页尾就是最新，卡片不可能合法地"更晚"，
上界本来就多余）。

**同一家族的第二次**（`docs/bugs/028-document-card-not-persisted.md` 是第一次）：
028 是格式问题——时间都解析成 `Date` 了，但按**字符串**比（`Z` vs `+08:00` 差 8 小时）；
040 是数值问题——格式对了，但**两块表的读数本身**不同。同一个根：把时间戳当绝对坐标用。

**残留审计**：客户端目前拿时间做比较的只剩这条管线（其余用于纯展示）；
但"用户自己设的时间（提醒到期、计划时间）vs 服务器 now"是另一类问题，尚未统一口径。
