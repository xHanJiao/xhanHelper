# 右栏统一 tab（macOS）

> 状态：实现中（2026-11-18）
> 样张：`client/xHelper/scripts/snapshot-righttabs.swift`（真 SwiftUI 离屏渲染，出图见会话）

估算（开工）：6 面 / 3 缝 / 3 次真机往返 / 2 个待拍决定 → 14–24h

## 1. 背景

macOS 右栏有两套壳并存：

- **面板壳**：`browsePanelSelection` + `BrowsePanelView`，每个域自带「返回｜面板名｜搜索/刷新/模式」页头，不参与 tab 条；
- **浏览器壳**：`RightSidebarManager.tabs` + `activeTabId`，tab 条只在浏览器态出现。

结果是同一个右栏有两套导航心智（「返回上一层」vs「切 tab」），且内容区页头重复造轮子。

## 2. 决策（2026-11-18 用户拍板，全部按推荐项）

| # | 决策 |
|---|---|
| D1 | 一套 tab：右栏所有内容都在 tab 里；**首页 = 没有 tab**（tab 条上有一枚常驻「首页」chip 兜底可达） |
| D2 | tab 条放**右栏顶部**做胶囊条（不进顶栏）；每个 tab 的动作按钮上移到顶栏 center 槽 |
| D3 | 列表页是**单例 tab**；详情（文档/书/灵阅页/网页）是**独立 tab**，按对象 id 去重 |
| D4 | 详情 tab 排在父列表 tab 右边；详情里的「返回」= 激活父列表 tab，详情 tab 保留 |
| D5 | 词云是列表 tab 的 **mode**，不单开 tab |
| D6 | 切 tab 保活：当前 tab + 媒体保活网页 + **最近 3 个详情 tab** |
| D7 | 分屏语义不变：仍只允许网页 tab 进右窗格 |
| D8 | 不做跨启动持久化；iOS 不动 |

## 3. 落地要点

- `RightSidebarTab.kind`：`home / panel(BrowsePanel) / document / book / artifact / web / tool`；
  所有「这是不是网页」的判断收成 `isWebTab(id)`，替掉散落的 `specialTabIDs.contains` + `isArtifactTab`。
- 内容由 `RightSidebarContentFactory` 注入（`ContentView` 里接线，VMs 在那一层）：
  `home / panel(_) / document(_) / book(_)` 四个闭包；manager 只存 `AnyView`。
- 关 tab 的收尾放 `RightSidebarTab.onClose`：书（`openBook = nil` + 重扫书架）、文档（清选中 + 协调器 deselect）、
  列表（退出多选/搜索态）、工具页（无）、网页（provider session teardown，沿用原路径）。
- 顶栏：
  - `document-actions` 补「划线与批注」「查找」（读写 `@AppStorage` 同两个 key）；
  - 新增 `tab-actions`（priority 7，低于浏览器 10 / 详情 8）承载列表页动作：刷新、多选、导入、导出（回收站批量留在模式行，稀有且有二次确认）。
- 模式控件（列表/词云/回收站 + 读者层级 + 搜索图标）留在内容区一条 40pt 细行：
  搜索是「就地展开输入框」的视图内状态，塞进顶栏需要额外共享状态，V1 不动。
- 书中阅读页（`BookReaderView`）自带常驻 chrome（章号/目录/Aa）**本轮不动**，其「返回书架」= 激活书架 tab。
- 首页 chip 常驻 tab 条最左（`house` 图标），点它回首页。新空白页走 ⌘T / 首页「浏览器」卡 / 顶栏浏览器组 `+`。

## 4. 入口映射

| 入口 | 行为 |
|---|---|
| 首页卡片 / 问候行 | `openPanel(...)`（不存在则建，存在则激活） |
| ⌘1–5（顶栏导航组） | 同上 |
| 设置「内容与工具」 | 同上 |
| 聊天文档卡片 / `xhanhelper://document/<id>` | `openDocument(id:)` → 文档详情 tab |
| 灵阅卡片 / 抽屉 | 已有 `artifact:<id>` tab，不变 |
| 书架卡片 | 书详情 tab |
| ⌘W | 侧栏展开且无分屏：关当前 tab（网页走 provider，其余走 manager） |
| ⌘⇧[ / ] | 在原窗格内切 tab（含非网页） |

## 5. 验收

1. 右栏所有内容只在 tab 里出现，没有「返回｜面板名」页头；
2. 首页 ↔ 各 tab、文档列表 ↔ 文档详情、书架 ↔ 书详情都能来回；
3. 关 tab 收尾正确（书进度落盘、文档选中清掉、多选退出、网页 session 释放）；
4. 深色/浅色、840 / 380 宽下 tab 条可横向滚动、不撑破布局；
5. `swiftc -typecheck` 两个 target 通过；真机点一遍不崩。

## 6. 后续变更

- 2026-11-18 实现落地，与定稿的五处偏离：
  1. **模式/搜索控件留在内容区**：列表/词云/回收站分段、读者层级、搜索图标与就地展开的输入框仍是内容区一条 40pt 细行
     （搜索是视图内状态，塞顶栏要额外共享状态，V1 不动）。刷新/多选/导入/导出已上顶栏 `tab-actions`。
  2. **书阅读页 chrome 不动**：`BookReaderView` 自带常驻 chrome（章号/目录/Aa/查找）仍留在页内；
     其「返回书架」= 激活书架 tab。动作上顶栏留作后续。
  3. **tab 条用横向滚动**，没有 `⋯` 溢出菜单（省掉测量与变宽计算）；首页 chip 固定在左端。
  4. **⌘1–5 的空间关系未变**：导航组在顶栏 `right` 槽（priority 5），`tab-actions` 在 `center` 槽（priority 7），
     两槽互不挤占，`TopBarLayout` 只降级 `center`。所以列表 tab 里 ⌘1–5 理论上仍可用（真机未复核）。
  5. 首页 tab 在最左常驻（`house` chip，不可关），用来兜底"tab 条打开着怎么回入口网格"。
  影响：`RightSidebarManager` / `RightSidebarView` / `RightSidebarTabStrip`(新) / `SidebarHomeView`(新) /
  `BrowsePanelView`（改为 `PanelTabView`）/ `DocumentTabView`(新) / `RightSidebarTabActions`(新) /
  `DocumentListView` / `BookshelfView` / `PageHolders` / `DocumentSelectionCoordinator` / `ArtifactOpenCoordinator` /
  `BrowserToolProvider` / `BrowserFocus` / `TopBarView` / `ContentView` / `xHelperMac/App.swift`
- 2026-11-18 修补（评审后 + 真机反馈）：
  1. **tab 条尾部 `+` 删除**：新空白页仍有三个入口（⌘T / 首页「浏览器」卡 / 顶栏 `browser-actions` 的 `+`）；
     分屏右窗格那颗 `+` 保留（窗格内唯一入口）。
  2. **`PanelTabView` / `SidebarHomeView` 不再持有 `baseURL`/`token` 字符串**：tab 内容只建一次，
     本地后端重启会换 token（`DeviceManager.token` 是 `@Published`），改成持 `DeviceManager` 并实时取。
  3. `tab-actions` 的多选按钮按面板补回 `disabled`（空列表不可点），书架导入中显示沙漏并禁用。
     （顶栏按钮状态随注册表刷新，导入中的图标切换可能有几十 ms 延迟。）
- 已知遗留：`manager → tabs → AnyView → 视图 → manager` 存在强引用环（manager 生命周期 = 登录会话，
  登出时少回收一次，量级几 MB）；删除文档时其详情 tab 不会自动关（再激活会拉到 404）。
