# 记忆主题词云（macOS）

```
估算（开工）：~8 面 / 2 缝（0 需先建探针）/ 1 次真机往返 / 4 个待拍决定 · ~2–3h
估算（定稿）：  8 面 / 2 缝（0 需先建探针）/ 1 次真机往返 / 0 个待拍决定 · ~2h
```

> 定稿后待拍决定已由用户选定：仅 macOS / 点击筛选 / 带「≥N 篇」步进器 / 网络模型改名 `CloudTag`。

## 这功能是干嘛的

文档页有个「词云」：把一篇篇文档的标签聚在一起，出现越多的标签字越大，一眼能看出哪些主题最常写。记忆里每条也有一个 `topic`（主题词），但记忆页现在只有一列卡片，看不出「我都在记些什么」。

这个功能就是给记忆页也加一个「词云」模式：把记忆的 topic 铺成一张词云，点某个 topic 就筛出属于它的记忆。

## 长什么样 / 怎么用

- 记忆页头右上角多一个开关：**列表 / 词云**。默认还是列表，跟现在没差别。
- 切到词云：一屏铺满 topic，越常出现的字越大、越靠中间。旁边有个「≥ N 篇」的步进器，可以把只出现一两次的冷门 topic 滤掉，让图干净。
- 点某个 topic：切回列表，只显示这个 topic 的记忆；列表顶上出现一个筛选小胶囊，点 ✕ 清掉、回到词云。
- 纯 macOS。iOS 不动，还是原来的卡片列表。

## 为什么这么简单

一条记忆只属于**一个** topic（不像文档能有多个标签），所以：

- 文档词云那套「⌘ 多选 + 共现联动高亮」在这里**没有意义**——两条不同 topic 永远不可能同时出现在一条记忆里，交集恒为空。所以**不做**。
- 点击主题只做「按这一个 topic 筛选」，不做多选。

这不是偷懒省事，是这个场景本来就不需要。

## 怎么实现（复用为主）

**核心思路：把文档已经写好的词云组件搬出来共用，不重写。**

文档的词云画法（`TagCloudView` + `BallCloud`）只关心两件事：名字、出现次数。而记忆 topic 也正好是这两件事。所以整块搬到一个公共文件里，文档和记忆都用它。

后端更省：**聚合 topic 及其出现次数的查询早就有了**（`UserMemoryRepo.DistinctTopics`，主题清洗工具在用），直接拿来用，不新写查库逻辑。

### 改动清单

| # | 文件 | 改什么 |
|---|------|--------|
| 1 | `server/internal/service/user_memory_repo.go` | 列表查询 `ListByUser` 加一个 `topic` 过滤参数（现在是按关键词/来源筛，没有按 topic 筛）；`DistinctTopics` 原样复用 |
| 2 | `server/internal/controller/memory.go` | 列表接口读 `topic` 参数；新增一个「取所有 topic 及次数」的接口 |
| 3 | `server/internal/controller/router.go` | 注册 `GET /api/memories/topics` |
| 4 | `server/internal/tool/get_topic_detail.go`、`get_topic_stats.go` | `ListByUser` 签名多了一个参数，这两处调用补个空值 |
| 5 | `server/internal/service/user_memory_repo_test.go`（新） | 一个测试函数，覆盖「按 topic 筛」和「topic 聚合」两条 |
| 6 | `client/.../Shared/Views/WordCloudView.swift`（新） | 从 `DocumentListView.swift` 整块搬出词云的三个类型（`TagCloudView` / `PlacedTag` / `BallCloud`），改成公共可见；`DocumentTag` 改名 `CloudTag` |
| 7 | `DocumentListView.swift` | 删掉搬走的部分，改用公共组件；`APIClient.swift`、`DocumentViewModel.swift` 里 `DocumentTag` 同步改名 `CloudTag` |
| 8 | `client/.../Shared/Services/APIClient.swift` | 加 `MemoryTopic`（topic + count，附一行转成 `CloudTag`）；加取 topic 列表的请求；`listMemories` 支持 `topic` 参数 |
| 9 | `client/.../Shared/ViewModels/MemoryViewModel.swift` | 加「是否词云模式」「topic 列表」「≥N 阈值」「当前筛选的 topic」以及切换/清空方法 |
| 10 | `client/.../Shared/Views/MemoryListView.swift` | 列表/词云两个分支（`#if os(macOS)`）；列表顶部的筛选胶囊；`.onChange(of: showCloudMode)` 内聚加载 |
| 11 | `client/.../Shared/Views/BrowsePanelView.swift` | 记忆页头加「列表/词云」开关 + 「≥N 篇」步进器；切到词云时去拉 topic |
| 12 | `client/.../Shared/Views/PageHolders.swift` | `MemoryPage` 删掉重复的 `.task`（加载由 `MemoryListView` 负责） |
| 13 | 本文档 | — |

### 明确不做

⌘ 多选、共现边、词云内搜索高亮、iOS 词云、真实交集篇数统计（单 topic 不需要）。

## 验收

1. `cd server && go build ./... && go test ./...` 全绿。
2. 客户端双 target `xcrun swiftc -typecheck` 绿（agent 自检，不跑 xcodebuild）。
3. 真机：记忆页切「词云」→ 能看到 topic 按频次大小排布 → 拖「≥N 篇」过滤 → 点某 topic → 列表只剩它的记忆，点 ✕ 回到词云。
4. 文档页词云**行为不变**（这次改了它的组件归属，要回归看一眼）。

## 已知接受的简化

- 词云布局仍用文档那套「圆盘分行 + 面积缩放」，标签量级小够用；不做真正的碰撞检测。
- topic 次数聚合每次全量算（个人量级，万条再谈优化）。
- 记忆词云不做本地搜索高亮（文档有，记忆暂不需要）。
