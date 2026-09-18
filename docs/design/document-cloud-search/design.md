# 文档词云本地搜索高亮（macOS）

```
估算（开工·补录）：~7 面 / 2 缝（0 需先建探针）/ 2 次真机往返 / 3 个待拍决定 · ~1–2h
估算（定稿）：  7 面 / 2 缝（0 需先建探针）/ 1 次真机往返 / 0 个待拍决定 · ~0.5–1h
```

> 开工行是事后补录（本票未在需求首次说出口时跑 `/estimate`）：按第一条需求消息时刻可知的信息回填，当时三个决定（高亮 vs 筛选 / 输入框位置 / 匹配规则）均未拍。

## 目标

macOS 文档页「词云」模式下，能搜索特定标签并**本地高亮**（不连后端）。命中词用 `appAccent` 强调，其余词变淡，**布局不重排**——打字时词云不跳动。

## 已确认的设计决策（勿改）

1. **高亮，不筛选**：命中词强调、未命中词降透明；`BallCloud.place` 不重跑，词云布局与位置保持稳定。
2. **复用页头 🔍**：词云模式下点页头搜索图标打开同一个输入框，行为变成本地实时高亮、**不自动收起**；列表模式下仍是原来的后端标题/正文检索。
3. **匹配规则**：子串包含、忽略大小写（`localizedCaseInsensitiveContains`）。
4. **关闭搜索面板不清查询**：高亮保留，🔍 保持 active；清空按钮才清除。
5. 词云与模式选择器本就 `#if os(macOS)`，iOS 不受影响。

## 改动清单

| # | 文件 | 改动 |
|---|------|------|
| 1 | `Shared/ViewModels/DocumentViewModel.swift` | 新增 `@Published var cloudQuery = ""`（纯 UI 状态，与 `mode`/`minTagCount` 同处） |
| 2 | `Shared/Views/BrowsePanelView.swift` | `toggleDocSearch` 按模式回填草稿；页头 🔍 active 条件按模式取字段；`transientSearchPanel` documents 分支按模式分叉；`TransientSearchPanel` 加 `live` 参数（隐藏「搜索」按钮 + `.onChange` 实时回调） |
| 3 | `Shared/Views/DocumentListView.swift` | `cloudContent` 传 `query`；`TagCloudView` 加 `query` 并按命中改前景色/透明度（**不改 weight、不触发 rebuild**） |

## 关键点

- **命中样式不动布局**：只改 `foregroundColor` 与 `opacity`，不改 `font`/`weight`（改 weight 会让实际渲染宽度超过 `measure` 的测量值，可能重叠）。
- **不监听 query 变化去 rebuild**：`TagCloudView` 只在 `size` / `tags` 变化时重排。
- `TransientSearchPanel.live` 默认 `false`，账单与列表文档的调用点零改动。

## 人工验收清单

1. 文档 → 词云，点页头 🔍 → 出现输入框，输入标签片段：命中词变 accent、其余变淡，**布局不跳动**；边输入边生效。
2. 清空按钮 → 全部恢复常态；关闭 🔍 面板 → 高亮保留且图标仍高亮。
3. 无命中时全部变淡（有明确反馈）。
4. 切到「列表」模式点 🔍 → 仍是原来的后端「按标题/正文检索」。
5. iOS 文档页无变化。

## 已知接受的简化

- 不显示命中计数。
- 调暗透明度为硬编码经验值（~0.2），若真机观感不佳再调。
