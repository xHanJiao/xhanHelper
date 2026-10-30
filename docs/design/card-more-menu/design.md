# 卡片右上角「⋯」：把管理类动作收进一个菜单

> 日期：2026-10-30
> 估算（开工）：7 面 / 2 缝（0 条需先建探针）/ 1–2 次真机往返 / 3 个待拍决定 · ~3–6h
> 估算（定稿）：7 面 / 2 缝（0 条需先建探针）/ 1–2 次真机往返 / 0 个待拍决定 · ~4–6h

## 1. 人话版

右栏三个列表页（文档 / 灵阅 / 记忆）的卡片，现在动作分散在三处：文档的分享/下载/导出 PDF 在**窗口顶栏**（还得先打开文档详情才出现），灵阅的删除在**卡片页脚**，记忆的 👍/👎 也在页脚。四件事塞进同一行页脚摆不下（260 宽的三列卡片，页脚可用约 232pt，日期 + 四个带文字按钮要 250pt+）。

改法是：**卡片右上角放一颗 `⋯`，点开下拉菜单，管理类动作全在里面**。页脚不再放管理按钮。

- **文档卡片**：`⋯` = 分享链接 / 下载 Markdown / 导出 PDF / ─── / 删除（**仅 macOS**，见 §3.4）
- **灵阅卡片**：`⋯` = 分享链接 / 下载 HTML / 导出 PDF / ─── / 删除
- **记忆卡片**：不变（页脚 👍/👎，**不加 `⋯`**；记忆不做删除）
- 智能体卡片、回收站的「恢复」：不变

**范围只限卡片列表页。详情页（文档全文页、灵阅产物页）一行代码不碰**：顶栏那 3 颗文档按钮、灵阅详情右上浮的分享菜单全部保留原样。所以卡片上的 `⋯` 是**新增的第二入口**，不撤任何现有按钮。

已拍的三个决定（2026-10-30）：灵阅菜单**不放撤销分享**；文档与灵阅菜单项**完全一致**（分享/下载/导出/删除）；记忆**不要删除**，因此也不加 `⋯`。

## 2. 为什么是「右上角菜单」而不是别的摆法

| 方案 | 为什么不选 |
|---|---|
| 页脚塞 4 个图标 | 260 宽卡片没余量；纯图标无文字，删除和导出分不清 |
| 页脚「2 个常驻 + ⋯」 | 卡片宽度只有三档，为它写一套按宽度折叠的纯函数不划算；且仍然占页脚 |
| 动作只放详情页 | 列表里想直接分享一张文档要先进详情再返回，多两次点击 |
| hover 才出现按钮 | iOS 没有 hover，等于不可发现 |

菜单的额外好处：**有文字标签**（图标 + 文案，删除项走系统红并单独一组）、**跨端一致**（iOS 也能用）、**危险操作离高频点击区最远**。

## 3. 技术方案

### 3.1 组件层（两处）

`Shared/Views/CardStyle.swift`

- `CardAction` 加 `help: String?`（菜单项 tooltip / 无障碍名）。`id` 由 `UUID()` 改为 `"\(icon)|\(label)"` 计算属性——原来每次 body 求值都换身份，`ForEach` 会把按钮当新视图重建。
- `CardConfig` 加 `menuActions: [CardAction] = []`。**`actions` 保持原义**（页脚按钮，现在只有记忆的 👍/👎 用），两者互不干扰。

`Shared/Views/CardView.swift`

- 右上角 `Menu`（`menuStyle(.borderlessButton)` + `menuIndicator(.hidden)`，照 `TopBarView.swift` 里 `CapsuleOverflowMenu` 的写法），**仅 `menuActions` 非空时渲染**；低对比常显（约 40% 透明度，hover 到 75% + 淡底），不做「hover 才出现」。
- 标题加 22pt 右侧避让，避免两行标题钻到 `⋯` 底下。
- 菜单渲染：第一个 `role == .destructive` 的项前插 `Divider()`，并把 role 传给 `Button(role:)` 走系统红。
- `⋯` 按钮 `.help("更多操作")`。

**选择态让位**：不新增开关，调用方在选择态传 `menuActions: []`（和现在灵阅「多选时把 `actions` 置空」同一招，只是从页脚挪到了菜单）。

### 3.2 文档卡片

`Shared/Views/DocumentListView.swift` 的 `documentCard` 加 `menuActions`（选择态 `[]`）：

| 项 | 调用 | 备注 |
|---|---|---|
| 分享链接 | `DocumentSelectionCoordinator.shared.share(doc)` | 现成方法，幂等（先查旧链再建） |
| 下载 Markdown | `...download(doc)` | 现成方法（`NSSavePanel`） |
| 导出 PDF | `...exportPDF(doc)` | **需参数化**，见下 |
| 删除 | `pendingDelete = doc` | 与右键菜单共用同一个确认弹窗和文案 |

`Shared/Services/DocumentSelectionCoordinator.swift`：`exportPDF()` 由无参私有改成 `exportPDF(_ doc: Document)`——卡片上的文档不是「当前选中」的那篇。顶栏按钮改成先取 `selectedDocument` 再传参，行为不变。

右键菜单**保留**（macOS 习惯），与 `⋯` 共用 `pendingDelete`。

### 3.3 灵阅卡片

`Shared/Views/ArtifactListView.swift`：页脚的 `CardAction(删除)` 整段搬进 `menuActions`，并补三项：

| 项 | 实现 |
|---|---|
| 分享链接 | `ArtifactOpenCoordinator.shared.copyShareLink(id:)`（新增） |
| 下载 HTML | `ArtifactOpenCoordinator.shared.downloadHTML(id:title:)`（新增） |
| 导出 PDF | `ArtifactOpenCoordinator.shared.exportPDF(id:title:)`（新增） |
| 删除 | 原样：`pendingDeletion = .one(item)` |

`Shared/Services/ArtifactOpenCoordinator.swift` 新增三个方法：

- `copyShareLink(id:)`：复用现成的 `share(id:)`（服务端幂等：有 token 就复用）→ `copyToPasteboard` → toast。这段逻辑现在长在 `HTMLArtifactView.copyShareLink` 里，详情页那颗浮钮保持不动（两份实现共存，见「明确不做」）。
- `downloadHTML(id:title:)`：`getArtifact(id:)` 拿正文（列表接口不含 html）→ `NSSavePanel` → 存 `.html`。
- `exportPDF(id:title:)`：`getArtifact(id:)` → `injectingBaseline(html)`（与屏幕同一份版式基线，`HTMLArtifactView.swift` 里现有的私有函数放宽为 internal）→ `DocumentPDFExporter.exportHTML` → `NSSavePanel`。

`Shared/Services/DocumentPDFExporter.swift` 加一个入口：

```swift
static func exportHTML(_ html: String, to url: URL, baseURL: URL? = nil) async throws
```

内部就是现成的 `renderPDF(html:baseURL:)` + 写盘——文档导出那条 `buildHTML(markdown)` 的管线不动。

### 3.4 平台差异

- 分享链接：两端都能用（`copyToPasteboard` 有 iOS 分支）。
- 下载 / 导出 PDF：依赖 `NSSavePanel`，**macOS only**；iOS 的灵阅菜单里只出现「分享链接 / 删除」。
- **文档卡片整颗 `⋯` 仅 macOS**：文档的分享/下载/导出在 `DocumentSelectionCoordinator` 里本身是 `#if os(macOS)`，删除的确认弹窗（`confirmationDialog`）也是 macOS-only——iOS 放出来就是几个点了没反应的项，所以 iOS 文档卡片保持现状（无 `⋯`）。

## 4. 影响面

- 主链路？否。纯客户端展示层 + 一张新菜单；**后端零改动**（`artifacts` 的 share/unshare/delete、`documents` 的 share/delete 都已存在）。
- 详情页行为不变（灵阅详情那颗浮钮、文档顶栏三颗胶囊都不动），所以不存在「同一动作两处文案打架」的新问题。
- 记忆卡片零改动。
- DB / 接口 / 配置：无。

## 5. 验证

1. `xcrun swiftc -typecheck`（Shared + xHelperMac 两个源集，带项目设置）。
2. 真机（必须）：
   - 文档卡片 `⋯` 四项都能跑通；删除走原有确认弹窗，删完进回收站；回收站卡片仍只有「恢复」。
   - 灵阅卡片 `⋯` 四项；下载的 `.html` 用 Safari 打开观感与右栏一致；导出的 PDF 分页正常（见风险 2）。
   - 灵阅多选态：`⋯` 让位给勾选圈、不误触。
   - **点 `⋯` 不会连带打开卡片**（见风险 1）。
   - 记忆卡片页脚 👍/👎 行为不变、无 `⋯`。
3. 深浅色各看一次 `⋯` 对比度。

## 6. 明确不做

- 不动详情页（文档顶栏三颗胶囊、灵阅详情浮的分享菜单）。
- 不做记忆删除；记忆卡片不加 `⋯`。
- 不合并两处分享实现（`HTMLArtifactView.copyShareLink` 与 coordinator 的 `copyShareLink` 暂时并存）。
- 不统一「分享链接 host 规则」（文档用 `DeviceManager.webBaseURL`、灵阅用 API base；两者都是 `/api/share/...`，dev 走 Vite 代理、线上同源，当前都可用；自定义后端场景才分叉，另开票）。
- 不做按宽度自动折叠（卡片宽度档位太少，不值）。
- 智能体卡片的编辑/删除不搬进 `⋯`（后续想搬是零成本，改传 `menuActions` 即可）。

## 7. 风险 / 回滚

1. **`Menu` 挂在带 `.onTapGesture` 的卡片里**，点开菜单是否连带触发卡片点击。页脚普通 `Button` 现在不受影响，`Menu` 需真机点一次。回滚：给 `⋯` 外层加 `.contentShape` / 换 `Button` + `Menu` 触发方式。
2. **灵阅导出 PDF 的分页**：产物页有 `body{min-height:100vh}`（`HTMLArtifactView` 的 baselineCSS），打印引擎可能把页面拉长或留白。真机看一版；实在不行就让 `exportHTML` 用一个打印专用基线（去掉 `min-height`）。
3. 卡片 260 宽时右上角 `⋯` 与两行标题的留白可能偏挤——真机截图再调（只改一个 padding 常量）。
4. 回滚：`menuActions` 有默认值 `[]`，删掉三个调用点传参即回到现状；组件改动本身不影响任何未使用的卡片。

## 8. 审查修正（2026-10-30，`docs/code-review/card-more-menu/2026-10-30.md`）

首版实现通过真机验证后，代码审查提了 4 条 P2 + 5 条 P3；采纳 6 条，改动都在原文件内：

| 审查项 | 处理 |
|---|---|
| P2-1 下载的 `.html` 没注入版式基线（与右栏两个样子） | `downloadHTML` 改走 `injectingBaseline(html)` 落盘 |
| P2-2 先拉正文后弹保存面板（大页面像点了没反应） | 下载 / 导出都改成**先面板后拉**，对齐 `DocumentSelectionCoordinator.download` |
| P2-3 `⋯` 对比度 ~2.4:1、无无障碍名 | 常态 opacity 0.4→0.55（hover 0.75 不变）+ `.accessibilityLabel("更多操作")` |
| P2-4 内联 base64 大图可能在 `didFinish + 350ms` 内没解码完 | `renderPDF` 加 `waitForImages`（默认 false，文档链路不变）；`exportHTML` 传 true，打印前用 `callAsyncJavaScript` 等所有图片 `complete` + `decode()` |
| P3-7 `CardAction.id = "icon|label"` 会撞 | 两处 `ForEach` 改用 `indices`（`CardAction.id` 保留，不再被用于身份） |
| P3-8 `.help("")` | 兜底 `action.label.isEmpty ? "操作" : action.label` |

不采纳：P3-6（两处分享实现并存，§6 已列为已知债务）、P3-5 / P3-9（`snapshot-chatrows*` 脚本与 `project.pbxproj` 字段顺序都不是本 feature 的改动）。

重验：macOS / iOS 两次 `swiftc -typecheck` 均 0 error；真机只需重点看「大页面导出 PDF 图片是否齐」「`⋯` 加深后是否碍眼」。
