# 文档划线 · 点击定位到右侧批注面板

> 状态：**已实现**（2026-10-06）
> 范围：仅 macOS 客户端（WKWebView 模板 JS + SwiftUI 批注面板）。服务端 **零改动**。
> 前序：`docs/design/document-highlight-annotation/design.md`（划线与批注本体；本方案改其 D9 / §8.3）
> 唯一入口：macOS 客户端。iOS / Web / 分享页 **不做**（延续 D1）。

---

## 一、背景与目标

现状（已核实）：

| 方向 | 现状 |
|---|---|
| 正文划线 **hover** | 出 popover 显示 note（`DocumentReaderView.swift` JS `mousemove`） |
| 正文划线 **左键点击** | 出 `#ctxmenu` 下拉：编辑批注 / 复制原文 / 删除划线（D9） |
| 右侧卡片 **hover** | 画「卡片 → 划线」连线 |
| 右侧卡片 **点引文** | 正文滚到该划线并闪烁（`onJump` → `scrollToAnnotation`） |
| **正文划线 → 右侧卡片** | ❌ **不存在** |

面板是第三列常驻面板（`AnnotationPanel`，宽 300）。（当时）两种视图：**讨论**（卡片 + 可折叠线程）/ **全部**（扁平列表，三 tab）——**2026-10-06 已合并为单视图**，见 `docs/design/document-annotation-single-view/design.md`。它现在是纯展示的 `ScrollView { LazyVStack { ForEach } }`，**没有 `ScrollViewReader`、每行也没挂 `.id`** —— 无法程序化滚动到指定条目。

目标：**正文里点一条带批注/划线的文字 → 右侧面板里对应的那条卡片/行滚动到视野中央并闪烁一下**，让「正文标记 ⇄ 右侧条目」双向可达（反方向已有：卡片→正文）。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| AF-1 | 左键职责 | ✅ **左键点击划线/气泡 = 定位到右侧对应条目**（滚动 + 闪烁）。不再开菜单 |
| AF-2 | 右键职责 | ✅ **右键划线/气泡 = 弹出编辑批注 / 复制原文 / 删除划线菜单**（拦截 `contextmenu` 并 `preventDefault`）。原左键菜单整体搬到右键 |
| AF-3 | 面板形态 | ✅ 保持常驻第三列；**不切视图** —— 就滚/闪当前视图里的卡片。用户拍板「不切模式」。（2026-10-06 两视图合并后只剩讨论卡片。） |
| AF-4 | 折叠态 | ✅ 面板若被收起（`annotOpen=false`），点击划线**自动展开**；受窗宽硬地板限制时仍可能只展开成窄条（已知上限） |
| AF-5 | 重叠划线 | ✅ 取点击命中的最内层 `mark` 的 `data-id`（与旧菜单一致）。不做「菜单里选哪条」 |
| AF-6 | orphan 划线 | ✅ 正文里 orphan 本就不画，无法从左键触发；只能从列表右键菜单操作（既有行为不变） |
| AF-7 | 数据流 | ✅ 单向事件 + 单调递增序号：JS `post({type:'focus', id})` → controller `focusRequest = {id, tick}` → 面板观察。**不回写 JS**，无需对账 |
| AF-8 | 闪烁生命周期 | ✅ 面板本地 `flashID`，`onChange`/`onAppear` 触发后 ~1.2s 自动清；用 `tick` 保证连点同一条也能重新触发 |
| AF-9 | 服务端 | ✅ **零改动**。纯客户端交互 |

---

## 三、交互设计

### 3.1 左键 = 定位

点正文里带批注/划线的文字（或右侧小圆点气泡）：

1. `hideCtx()` + `hidePopover()` 收掉可能开着的浮层。
2. JS 发 `post({ type: 'focus', id })`。
3. Swift 侧：若面板收起 → `annotOpen = true`；把 `focusRequest` 递给面板。
4. 面板按当前视图滚动到该 `id` 并闪烁。

同时保底：正在选词的左键（`getSelection()` 非折叠且非空）**不触发定位**（沿用旧守卫），避免拖选划线内容时误跳。

### 3.2 右键 = 菜单

`content` 上新增 `contextmenu` 监听：

- 命中 `mark.hl` / `.ann-bubble` → `e.preventDefault()` + 在原光标处展开 `#ctxmenu`（沿用旧定位代码）。
- 未命中 → 不拦截，正文空白处的系统菜单照常。
- **任何右键先 `hideToolbar()`**，且 `mouseup` 只认左键（`e.button === 0`）：WebKit 右键会先自动选中光标下的词（给查询/服务用），不判键的话右键划线会同时冒出「选区工具条 + 右键菜单」两个浮层。右键顺手 `clearSel()` 清掉那个自动选区。

菜单/ hover 浮层互斥逻辑不变：菜单打开期间 `mousemove` 早退，不再弹 hover 浮层。

### 3.3 与 hover 的关系

hover 浮层（看 note 文字）与左键定位（找到右侧条目）互补，不冲突：hover 出浮层，点击跳右侧。

---

## 四、数据流 / 消息协议

新增 **JS → Swift** 消息一条（既有 JS→Swift 表见前序文档 §7.3）：

| 消息 | 载荷 | 用途 |
|---|---|---|
| `focus` | `{id}` | 左键点划线/气泡：请求右侧面板定位到该批注 |

Swift → JS 侧不新增（定位到正文的 `scrollToAnnotation(id)` 已存在，仍用于「卡片→正文」反方向）。

`AnnotationFocus`（`Shared/Views/AnnotationDrawer.swift` 顶层，两 target 可见）：

```swift
struct AnnotationFocus: Equatable {
    let id: String
    let tick: Int   // 单调递增；连点同一条时用于重新触发
}
```

`DocumentReaderController`：

```swift
@Published private(set) var focusRequest: AnnotationFocus?

// apply(...) 中：
case "focus":
    guard let id = message["id"] as? String, !id.isEmpty else { return }
    focusRequest = AnnotationFocus(id: id, tick: (focusRequest?.tick ?? 0) + 1)
```

---

## 五、实现面

| 文件 | 改动 |
|---|---|
| `client/xHelper/xHelper/Shared/Views/DocumentReaderView.swift` | JS 模板：左键 → `focus`；新增 `contextmenu` 监听收菜单；`apply` 加 `case "focus"` + `focusRequest` |
| `client/xHelper/xHelper/Shared/Views/DocumentListView.swift` | `onChange(reader.focusRequest)` → 收起则展开；把 `focusRequest` 透传给 `AnnotationPanel` |
| `client/xHelper/xHelper/Shared/Views/AnnotationDrawer.swift` | 顶层 `AnnotationFocus`；`AnnotationPanel` 讨论卡片加 `ScrollViewReader` / 行 `.id(a.id)` / `flashID` 闪烁；`AnnotationThreadCard` 加聚焦描边；补聚焦态 `#Preview`（§9 变更后只剩讨论卡片承接，`AnnotationListBody` 已删） |
| `client/xHelper/scripts/check-reader-html.sh` | 改「点划线出菜单」为「点划线发 focus / 右键出菜单」；菜单动作改用 `contextmenu` 触发 |

服务端、数据模型、API、生成逻辑 **全部不动**。

---

## 六、边界与已知上限

- （原「列表模式下焦点条目不在当前 tab 的 `visible`」这个边界已于 2026-10-06 随两视图合并消失 —— 现在只剩讨论卡片，无条件可达。见 §9。）
- 窗宽低于硬地板导致面板只能收成窄条时，`annotOpen=true` 也未必真正展开到位。
- orphan 划线正文里没画，左键点不到；仍从列表右键菜单处理。
- WKWebView `loadHTMLString` 场景下 `contextmenu` 的 `preventDefault()` 能否压掉系统菜单需**真机确认**；若压不掉，再考虑原生 `WKUIDelegate` 兜底。

---

## 七、自查与验证

- `client/xHelper/scripts/check-reader-html.sh`：
  - 左键点划线 → 断言 `post focus` 且 `id` 正确、`ctxmenu` 未打开。
  - 右键（`contextmenu` 事件）→ 断言菜单打开、菜单开着时 hover 不出浮层。
  - 菜单动作（复制/删除/编辑）改用 `contextmenu` 打开后再点。
- `swiftc -typecheck -D DEBUG`（macOS 26.3 + iOS 26.0）0 error。
- 真机：左键划线 → 讨论/全部两种模式下右侧对应条目滚动 + 闪烁；右键划线 → 菜单三项仍可用；连点同一条能重复闪。

---

## 八、文档同步

前序 `docs/design/document-highlight-annotation/design.md` 需同步：

- 决策表 **D9**：由「点击划线 → 菜单」改为「**左键 = 定位右侧批注；右键 = 编辑/复制/删除菜单**」。
- **§8.3 hover / 点击** 描述同步。
- JS→Swift 消息表加 `focus`。

---

## 附：后续变更

### 2026-10-06 · 落地与自查修复

- **落地**：左键定位 / 右键菜单已实现并通过 `swiftc -typecheck`（macOS + iOS）+ `check-reader-html.sh`（176 断言）+ `snapshot-reader.sh`。
- **前序文档已同步**：`docs/design/document-highlight-annotation/design.md` 的 D9、§8.3、JS→Swift 消息表已更新，并在其尾部加了「后续变更 · 2026-10-06」。
- **评审修复（同日）**：
  - 陈旧请求重复滚动/闪烁：新增 `AnnotationPanel.appliedTick`（面板重建复位），`applyFocus` 按 tick 去重。（当时列表分支 `AnnotationListBody` 经 `@Binding appliedTick` 共用同一份状态；该视图已于 2026-10-06 删除。）
  - 滚动可靠性：滚动 + 闪烁统一**延后一帧**（`Task { @MainActor }`），对齐 `DocumentListView.swift` 既有先例；`onChange` / `onAppear` 两条路径都延后。
  - 去重：两处 `applyFocus` 抽成文件级共享函数 `applyAnnotationFocus(_:proxy:appliedTick:flashID:)`；`Task.sleep` 的取消加了「有意降级」注释。
  - 可读性：合并 `DocumentListView` 相邻的两个 `#if os(macOS)` 块。
- **未处理（低优先）**：闪烁 `Task` 未显式随视图 `cancel()`（挂起 1.2s 后写已失效 `flashID`，SwiftUI 下无害）。

### 2026-10-06 · 批注面板合并为单视图（focus 不受影响）

- 面板原「讨论 / 全部」两视图合并为**单视图**（只留讨论卡片），`AnnotationListBody` 与三 tab 已删。**focus（点划线定位）行为不变**，现只由讨论卡片承接；`AnnotationPanel.appliedTick` 去重仍生效（不再有「切视图」场景）。
- 本文 §1「现状」、§5 实现面、§6 边界的旧描述已就地加注；详见 `docs/design/document-annotation-single-view/design.md`。

### 2026-10-06 · 右键不再叠出「选区工具条」

- **现象**：右键划线同时弹出两个浮层——顶部「划线/批注/复制」（选区工具条）与下方「编辑批注/复制原文/删除划线」（右键菜单）。
- **根因**：`mouseup` 只判「有没有非折叠选区」、不判鼠标键；WebKit 右键会先把光标下的词自动选中 → 同一个右键既触发 `contextmenu`（弹菜单）又让 `mouseup` 拿到那个自动选区（弹工具条）。
- **修复**（`DocumentReaderView.swift` JS 模板）：`mouseup` 开头 `if (e.button !== 0) { clearSel(); return; }`（工具条只服务左键选区，并清掉右键留下的自动选区）；`contextmenu` 一律先 `hideToolbar()`。
- **验证**：`check-reader-html.sh` PASS（168→171，新增「右键 mouseup 不弹工具条」「右键顺手清掉自动选区」「右键时收起选区工具条」）；`snapshot-reader.sh`；`swiftc -typecheck`（macOS + iOS）0 error。
