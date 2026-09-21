# 右侧栏「事实」面板（facts-panel）

估算（开工）：4 面 / 2 缝 / 1–2 次真机往返 / 2 个待拍决定 · 2–4 小时
估算（定稿）：4 面 / 2 缝 / 1 次真机往返 / 0 个待拍决定 · 3–4 小时

## 1. 目标

事实系统（[facts-system](../facts-system/design.md)）只写不读——库里在攒数据，但没有任何地方能看。
本功能给 macOS 右侧栏浏览面板加一个「事实」入口：**只读列表**，复用现成的卡片列表体系。

不做：搜索、编辑、删除、详情浮层、`fields`/`due_at` 展示、changelog 展示、iOS 入口。
理由：先把「能看到」这一步做出来，观察期靠肉眼校验抽取质量；其余等观察结果再说。

## 2. 已拍决定

| # | 决定 | 结论 |
|---|---|---|
| 1 | 卡片形态 | **B · `subject` 作标题**，`content` 作副标题，标签只放 `kind`（状态/事件），右下角 `updated_at` 相对时间 |
| 2 | 事实图标 | SF Symbol **`lightbulb`** |
| 3 | 网格窄栏回退 | 宽 ≥480 → **5 列 × 2 行**；窄 → **2 列**，网格由「不滚动」改为**可滚动**（10 格 5 行靠滚动兜住） |

交互预览：[mockup.html](./mockup.html)（控制条可切形态/宽度/图标/深浅色/字号）。

## 3. 后端：新增只读接口

`FactRepo.ListByUser(ctx, userID, limit, offset) ([]model.Fact, int64, error)` **已存在**（storage 段落地的），
只差 handler / 路由 / 接线，无 repo 改动。

```
GET /api/facts?limit=50&offset=0      （Bearer 认证，同 /api/memories）
→ { "status":"ok", "data": { "items":[Fact...], "total": N } }
```

- 只回 `status=active`，`created_at DESC`（沿用 repo 现有语义；**不按 updated_at 排**——UPDATE 原地覆盖会打乱分组观感，等真机看过再说）。
- 空结果必须回 `[]` 而不是 `null`：Go 的 `var list []T` 零值是 nil → marshal 成 `null` → Swift `[AppFact]` 解码抛错。
  在 handler 里做一次 nil→空切片归一（**不动 repo**，避免影响其它调用方）。
- `Fact` 结构体直接序列化，客户端只取需要的字段（`content_hash` / `status` / `fields` 多出来无妨）。

| 文件 | 改动 |
|---|---|
| `server/internal/controller/fact.go`（新） | `FactHandler` + `NewFactHandler` + `ListFacts`，仿 `memory.go:25`：err 先 `log.Printf` 再 `view.JSONError(c, 500, ...)` |
| `server/internal/controller/router.go` | `RouterConfig` 加 `FactHandler` 字段；`auth.GET("/facts", ...)` 放 `/memories` 旁 |
| `server/internal/appinit/router.go` | `controller.NewFactHandler(repos.Fact)` + 塞进 `RouterConfig` |
| `server/internal/controller/fact_handler_test.go`（新） | sqlite 建 `Fact` 表 → active ×2 + deleted ×1 → 断言只回 active、total 正确、空用户回 `[]` 不报错 |

## 4. 客户端：复用卡片列表体系

现成链路：`PageContainer`（loading/error/empty 三态）+ `CardGridView`（`maxColumns`/`minCardWidth`/`onLoadMore`）+ `CardConfig`（title/subtitle/tags/footerText/onTap）。

| 文件 | 改动 |
|---|---|
| `Shared/Services/APIClient.swift` | `listFacts(limit:offset:)` + `AppFact` / `AppFactListResponse`（`CodingKeys` 转 snake_case） |
| `Shared/ViewModels/FactViewModel.swift`（新） | 照抄 `ThemeViewModel`：`load` / `loadMore` / `hasMore` / `offset` |
| `Shared/Views/FactListView.swift`（新） | `PageContainer` + `CardGridView(maxColumns: 3, minCardWidth: 260)` + `CardConfig`；标题 `subject`、副标题 `content`、标签 `[kind 中文]`、footer `formatConversationDate(updated_at)`（复用 `ConversationListView.swift:85`）；`style` 直接复用 `.memory` 预设（同 `appAccent`，**不新增一个一模一样的 `CardStyle`**） |
| `Shared/Views/PageHolders.swift` | `struct FactPage` |
| `Shared/Views/BrowsePanelView.swift` | `BrowsePanel` 加 `case facts = "事实"`（图标 `lightbulb`、help 文案）；`factVM` 入参 + 4 个 switch（`isLoading`/`reload`/`panelContent`/新增）补分支；`grid` 改 5 列 + 加 `ScrollView`；`adaptiveColumns` 参数化列数 |
| `Shared/Views/RightSidebarView.swift` | 透传 `factVM` |
| `Shared/Views/ContentView.swift` | macOS `init` 加 `_factVM`；`RightSidebarView(...)` 传参 |

网格位置：**「事实」插在「记忆」右边**（语义相邻；将来记忆下线时位置不变）。

`kind` 中文映射：`state → 状态`、`event → 事件`，其余原样。徽章色用 `Color("accentSecondary")`（两 target 的 asset catalog 都有）。

### 网格算法（改 `adaptiveColumns`）

现在是「≥480 → 3 列 / <480 → 2 列」，注释写死「固定 8 格无需滚动」。10 格后：

| 宽度（可用宽 = 右栏宽 − 48） | 列数 | 格子 |
|---|---|---|
| ≥480 | 5 | 110 封顶，间距 `(w − 5×110)/4` 夹在 6…20；不够就压到 84 |
| <480 | 2 | 160 封顶，间距夹在 8…20；不够就压到 90 |

`grid` 由 `LazyVGrid`（无滚动）改为包一层 `ScrollView`——340pt 最小宽下 10 格 = 5 行，必须能滚。
网格内容用 `.frame(maxWidth: .infinity, minHeight: geo.size.height)` 撑满视口，保住原来「不足一屏时垂直居中」的观感。

## 5. 验收

- 后端：`go build ./... && go test ./...` 全绿（含新增 handler 单测）。
- 客户端：`swiftc -typecheck` 两 target 各一批（`BrowsePanelView`/`RightSidebarView`/`ContentView` 在共享源集，iOS 也要过）。
  注意 `ContentView.swift` 与 `xHelperMac/App.swift` 都是入口文件、天生在 typecheck 盲区之外——本功能不往入口挂 modifier，无需额外处理。
- 真机（用户在 Xcode）：跑一次聊天 → 右栏网格点「事实」→ 看真实宽度（340 / 560 / 840）下的 5 列观感、窄栏滚动、以及真实 facts 数据（`content` 长短差异大）在形态 B 下的卡片高度是否参差。

## 6. 已知边界

- 面板**不读 `facts.enabled`**：开关只控制后台抽取，库里有数据就该能看；关掉抽取时列表为空 → 走空态文案。
- 无搜索：`ListByUser` 无 keyword 参数，等真需要再加（先看观察期里数据量涨到多少）。
- 形态 B 下 `subject` 常常很短（"我"、"我妈"）→ 卡片上部会偏空，这是形态 B 的已知代价，已由 mockup 拍板接受。
