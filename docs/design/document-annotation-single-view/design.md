# 文档批注面板 · 合并为单视图（去掉「全部」列表）

> 状态：**已实现**（2026-10-06）
> 范围：仅 macOS 客户端（`AnnotationDrawer.swift` + `DocumentListView.swift` 装配）。服务端 **零改动**。
> 前序：`docs/design/document-highlight-annotation/design.md`（§8.6「划线列表抽屉」是「全部」视图的来源）；`docs/design/document-annotation-focus/design.md`。
> 唯一入口：macOS 客户端。iOS / Web / 分享页 **不做**。

---

## 一、背景与目标

右侧批注面板（`AnnotationPanel`，第三列常驻）现有**两个视图**，靠头部两个图标按钮切换：

| 视图 | 形态 | 独有能力 |
|---|---|---|
| **讨论**（默认） | 每条批注 = 卡片 + 可折叠线程 + 追问输入 | AI 回复 / 追问 / 展开线程（核心） |
| **全部** | 扁平行列表 + 三 tab（全部 / 仅批注 / 已失活） | ① 失活条目的「**重新定位 / 删除**」；② 三 tab 筛选 |

两个视图本质都是「同一份 `annotations` 的列表」，用户要**只保留一个**。

**目标**：删掉「全部」视图，只留「讨论」；把「全部」的**唯一不可替代能力**（失活条目重新定位）并入讨论卡片，不产生能力回退。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| SV-1 | 保留哪个视图 | ✅ 保留**讨论**（承载 AI 回复/追问/线程，核心）；删「全部」。反向不可取 —— 删讨论等于砍掉回复功能 |
| SV-2 | 切换入口 | ✅ 两个 mode 图标按钮**全删**（只剩一种视图，切换无意义）。头部只剩「标题 + 计数 + 收起」 |
| SV-3 | 失活条目「删除」 | ✅ **保留**，并入讨论卡片的失活条目（仅 `annotation.orphaned` 显示）。正文里**不画**失活划线，卡片上的删除是唯一入口。**2026-10-06 起取消「重新定位」**（用户拍板：手动重挂没啥用），见文末后续变更 |
| SV-4 | 三 tab 筛选（全部 / 仅批注 / 已失活） | ✅ **删**。失活靠卡片上的「已失活」徽标识别，够用；筛选对个人用量属过度设计 |
| SV-5 | 状态持久化 | ✅ 删 `@AppStorage("xhan_doc_annot_mode")` 及其读取。`UserDefaults` 里的旧 key 遗留无害，不做清理 |
| SV-6 | 点划线定位 | ✅ 不变（`focusRequest` / 闪烁仍由讨论卡片承接） |
| SV-7 | 数据源 | ✅ 不变。面板仍**只读** `DocumentReaderController.annotations`，自己不拉取、不缓存 |
| SV-8 | 服务端 / API / 模型 | ✅ **零改动**。`relocate` 接口与 JS 流程原样复用 |

---

## 三、交互设计

### 3.1 面板头部

```
划线与批注  5                              ›
```

去掉两个 mode 按钮，其余（标题、计数胶囊、收起 chevron）不变。

**2026-10-06 追加**：计数胶囊右侧、收起 chevron 左侧新增「**收起全部**」图标按钮（`arrow.down.right.and.arrow.up.left`，`help="收起全部回复"`）。仅当有已展开的线程（`expandedIDs` 非空）时才显示；点击收起全部，但**生成中的线程保留展开**。

### 3.2 失活卡片（`annotation.orphaned == true`）

保留原有的失活外观：引文灰 + 删除线、note 竖线灰、「已失活」红徽标。

**元信息行**（`已失活` 徽标 + 时间）右侧并入「删除」按钮（常显，不做 hover 才出现）：

- 形态：`[已失活] ……… 16:39 删除`
- 失活卡片**不给「让 AI 回复」入口**；已有线程时仍显示折叠行（`回复 N`）。
- **2026-10-06 起取消「重新定位」**（用户拍板「没啥用」）：手动重挂的客户端链路（按钮 / 重选提示 / `relocatingID` / `beginRelocate`·`cancelRelocate` / JS relocate 模式 / `APIClient.relocateAnnotation`）与 `check-reader-html.sh` 的 8 条断言一并删除；服务端 `POST /api/annotations/:id/relocate` 保留（不再被客户端调用）。**失活条目此后只能删除**。

### 3.3 非失活卡片

与现状**完全一致**（线程、追问、右键删除、悬停连线、点击跳正文——跳转热区为**整张卡片**，见「后续变更」）。

**2026-10-12 追加**：非失活卡片里，**「让 AI 回复」入口只给 `note` 非空的卡片**；纯划线（`note` 空、无线程）线程区整段不画（只剩引文 + 时间）。已有线程的纯划线仍显示 `回复 N` 折叠行。见文末「2026-10-12 · 纯划线不给『让 AI 回复』入口」。

---

## 四、实现面

| 文件 | 改动 |
|---|---|
| `Shared/Views/AnnotationDrawer.swift` | 删 `AnnotationPanelMode` 枚举、`mode` 绑定、`modeButton`、整个 `AnnotationListBody`；`AnnotationPanel.content` 只留讨论分支；`AnnotationThreadCard` 的失活删除并入元信息行；preview 去掉 `mode` 参数与「全部（列表）」预览，补「讨论 · 失活」预览 |
| `Shared/Views/DocumentListView.swift` | 删 `@AppStorage("xhan_doc_annot_mode")`、`annotationPanel` 里的 `mode:` 绑定；注释「视图模式」相应去掉 |
| `docs/design/document-highlight-annotation/design.md` | D10、§8.6、P4 完成情况表同步（「全部」视图取消、失活动作迁入卡片）；尾部「后续变更」 |
| `Shared/Views/DocumentReaderView.swift` | 仅改 JS 模板 `:1269` 注释措辞：「P4 抽屉里可见、可重挂」→「批注面板里可见、可重挂」（逻辑不动） |

服务端、数据模型、API、JS 逻辑 **全部不动**。

---

## 五、边界与已知上限

- 失活条目不再有「只看已失活」的筛选；个人文档失活量小，靠徽标翻找足够。真到量再谈。
- 失活卡片的引文点击（`onJump` → `scrollToAnnotation`）在正文里本来就定位不到，维持静默 no-op（既有行为）。
- 失活卡片**不给**「让 AI 回复」入口（已失活的线无法可靠答复）；已有线程仍显示折叠行可回看/追问（展开后输入框仍在）。

---

## 六、自查与验证

- `swiftc -typecheck -D DEBUG`（macOS 26.3 + iOS 26.0）0 error。
- 预览：讨论（折叠 / 展开 / 生成中 / 追问 / 定位闪烁 / **失活** / 空态）齐全。
- 不动 JS：`check-reader-html.sh` 复核 PASS。
- 真机：头部只剩一个收起按钮；失活卡片显示「删除」；非失活卡片与现状一致。

---

## 附：后续变更

### 2026-10-06 · 落地

- 按本方案实现：删 `AnnotationPanelMode` / `AnnotationListBody` / 两个切换按钮 / 三 tab / `@AppStorage("xhan_doc_annot_mode")`；失活动作并入 `AnnotationThreadCard`。净 **-225 行**。
- 验证：`swiftc -typecheck`（macOS + iOS）0 error；`check-reader-html.sh` PASS（176，JS 未动）。

### 2026-10-06 · 失活卡片排版微调（用户拍板）

- 原独立「动作行」在「已失活徽标 + 时间」行**之上**，观感散；改为：**失活徽标 + 时间 + 操作 同处一行**，并**去掉失活卡片的「让 AI 回复」入口**。
- 影响文件：`Shared/Views/AnnotationDrawer.swift`（`threadSection` 吸收 `orphanActions`）。
- 同步：`document-highlight-annotation`（D10 / §8.6 / P4 完成情况 / 后续变更）、`document-annotation-reply`（AR-14 / §7.1-7.3 / 附录 E / 后续变更）、`document-annotation-focus`（现状 / §5 / §6 / 后续变更）已就地更新。

### 2026-10-06 · 取消「重新定位」，失活条目只留「删除」（用户拍板）

- **决定**：手动重挂没啥用，删掉「重新定位」按钮及整条客户端链路。失活条目此后**只能删除**（服务端 `POST /api/annotations/:id/relocate` 保留，不再被客户端调用）。
- **删除的代码**：`AnnotationDrawer.swift`（卡片 `relocating` / `onRelocate` / `onCancelRelocate`、面板同名 props、preview 参数与「失活·重选中」预览）、`DocumentReaderView.swift`（`relocatingID` / `beginRelocate` / `cancelRelocate` / `relocate` / `case "relocate"`·`"relocateCancel"` / JS `relocateId`·`beginRelocate`·`cancelRelocate`·`relocateFromSelection`·Esc 监听·导出）、`APIClient.swift`（`relocateAnnotation`）、`DocumentListView.swift`（装配三行）、`check-reader-html.sh`（8 条重定位断言，176→168）。
- **不改**：D10 的**自动**三级重定位（正文改写后服务端重挂，与本手动流程无关）；失活灰态 + 删除。
- **验证**：`swiftc -typecheck`（macOS + iOS）0 error；`check-reader-html.sh` PASS（168）。

### 2026-10-06 · 跳转热区改为整张卡片（用户拍板）

- **决定**：原热区只有「引文」那一行（短引文时是窄条），太小；改为**整张卡片可点跳转**。
- **实现**：`AnnotationDrawer.swift` — 把 `.contentShape(Rectangle())` + `.onTapGesture { onJump() }` 从 `quoteBlock` 挪到 `AnnotationThreadCard` 根 `VStack`（`.clipShape` 之后）。卡片内 Button/TextField（折叠行、「让 AI 回复」、「删除」、追问输入框）优先消费自己的点击，故除它们外整卡任意处都可跳；`contextMenu`（右键）与悬停连线不受影响。
- **不改**：引文/批注文本内容与排版；`onJump` 目标逻辑不变。

### 2026-10-06 · 头部「收起全部」按钮（用户拍板）

- **需求**：面板里同时展开多条回复线程后，想一键全收；位置在计数胶囊右侧、收起 chevron 左侧。
- **实现**：`AnnotationDrawer.swift` — `AnnotationPanel` 加 `onCollapseAll` 注入回调；头部加图标按钮 `arrow.down.right.and.arrow.up.left`（`help="收起全部回复"`），**仅当 `expandedIDs` 非空时显示**，点击调用回调。`DocumentReaderView.swift` 加 `collapseAllThreads()`（`expandedAnnotationIDs.subtract(busyReplyIDs)` —— **生成中的线程不收**，与卡片折叠行的 `.disabled(busy)` 守卫一致）；`DocumentListView.swift` 装配 `onCollapseAll`。
- **不改**：单条 `onToggle` 折叠、追问输入框本地态；服务端 / JS / 数据模型不动。
- **验证**：`swiftc -typecheck`（macOS + iOS）0 error；真机点一下全部回复收起、按钮随之为空而隐去。

### 2026-10-12 · 纯划线不给「让 AI 回复」入口（用户拍板）

- **决定**：`AnnotationThreadCard` 的「让 AI 回复」入口加 `note` 非空守卫 —— 纯划线（`note` 空、无线程）线程区整段不画；已有线程的纯划线仍显示 `回复 N` 折叠行。详见 `docs/design/document-highlight-annotation/design.md` 文末同名条目。
- **影响文件**：`client/xHelper/xHelper/Shared/Views/AnnotationDrawer.swift`。服务端 / JS 零改动。
- **验证**：`swiftc -typecheck -D DEBUG`（macOS 26.3 / iOS 26.0）0 error。
