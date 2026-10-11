# macOS 中栏卡片吸顶（包住顶栏按钮 + 右栏去描边）

> 日期：2026-10-11
> 状态：**已实施**（真机验收待做）
> 关联：`../mac-two-column-layout/design.md`（双栏几何）、`../mac-apps/window-background-photos.md`（底图/卡片材质/顶栏几何）、
> `../mac-apps/topbar-slot-system.md`（顶栏左区让位）
> 参考：Xcode 主窗——左侧是一张**完整的、有底色的**卡片，红绿灯与会话下拉都在卡片内

估算（开工）：2 面 / 1 缝（观感只能真机看）/ 2 次真机往返 / 6 个待拍决定 · ~2–3h
估算（定稿）：4 面 / 1 缝（红绿灯探出标题栏容器 5pt、底色浓度只能真机看）/ 2 次真机往返 / 0 个待拍决定 · ~3–4h

## 一、目标

- **非 PiP**：中栏（唯一左列）是一张**完整卡片** —— 上边停在窗口内 `cardGap`(6) 处、与左/右/下对称，**四条边都看得见**，
  向上**溢出**把「红绿灯 + 会话下拉」包进卡片。
- **右栏**：不画边缘，内容直接坐在底图上。
- **右栏关着**时：中栏铺满整宽 → 顶栏**所有**按钮（DEV 徽标、中槽、右槽）都落在卡片里（**无需额外代码**）。
- **红绿灯**：与顶栏内容**同一行居中**（距窗顶 19）。
- **PiP**：红绿灯与会话下拉"释放"回底板 —— 中栏不渲染的自然结果，**零改动**。

## 二、几何（最终值，全部落在 `Layout`）

| 项 | 值 | 说明 |
|---|----|------|
| 卡片上边 | **6** = `cardGap` | 与左/右/下对称 → 上边看得见（早期方案停在 y=0，压在窗顶被窗口圆角切，已否，见 §七） |
| 卡片右缘 = 右栏开着时**按钮的边界** | 内容宽 − (`cardGap` 6 + seam 6 + 右栏宽) | 顶栏按钮簇**右对齐**，只能待在卡片右缘右侧那段；越过就是「按钮压在卡片上」（不变量，见 §八 2026-10-12 那条） |
| 卡片上溢量 | **40** = `topBarHeight − cardGap`（`cardTopRise`） | 描边/填充各 `.padding(.top, -cardTopRise)` |
| 顶栏内容距窗顶 | **12**（`topBarTopGap`） | 下边距仍 `toolbarVGap`(6) → 胶囊上方 12、下方 6 |
| 顶栏高 | **46** = 12 + 胶囊 28 + 6（`topBarHeight`） | 48 → 54（2026-10-11）→ **46**（2026-10-12 缩胶囊）；写死旧值的文档均已同步（见 §五） |
| 卡片底色 | **白霜提亮 6%**（`cardFillOpacity`，`Color.white`） | 与 `AppBackdrop.frost` 同族；**不能用 `assistantText` 压暗**（真机偏暗沉，见 §五） |
| 卡片投影 | **只在卡片外**的软阴影：浅色黑 10% / 深色白 10%，`blur 8` + `y 3`（`CardElevationShadow`） | 半透明卡片直接 `.shadow` 会渗进内部把里面压暗 → 先画不透明阴影形再**挖掉卡片内部** |
| 红绿灯 | 距窗顶 **19**（`Layout.trafficLightTopFromWindow`；`trafficLightNudge.y = 9 − 该值` = −10） | = 12 + 28/2 − 7（按钮半径）= 顶栏内容中心 26 − 7；改顶栏上边距/胶囊高自动跟随 |

## 三、方案（文件级）

> 数值随后续变更更新（当前值即本行数值）；变更历史见 §八。

1. **`LayoutConstants.swift`**：新增 `topBarTopGap`(12)、`cardTopRise`(40)、`cardFillOpacity`(0.06)；
   `topBarHeight` 改由「上边距 + 胶囊高 + 下边距」派生（46）。
2. **`TopBarView.swift`**：`.padding(.vertical, toolbarVGap)` → `.padding(.top, topBarTopGap)` + `.padding(.bottom, toolbarVGap)`
   （顶栏唯一来源，改它必须回头对 `topBarHeight`）。
3. **`ContentView.swift`（`macOSContentArea`）**：
   ```swift
   .background(alignment: .top) {
       RoundedRectangle(cornerRadius: 12)
           .fill(Color.white.opacity(Layout.cardFillOpacity))
           .padding(.top, -Layout.cardTopRise)
   }
   .modifier(CardElevationShadow(topExtension: Layout.cardTopRise))   // 投影：只在卡片外
   .overlay(
       RoundedRectangle(cornerRadius: 12)
           .strokeBorder(Color("assistantText").opacity(0.12), lineWidth: 0.5)
           .padding(.top, -Layout.cardTopRise)
           .allowsHitTesting(false))     // 溢出到顶栏那一截不许截走顶栏手势/空白双击缩放
   ```
4. **`CardBackground.swift`**：新增 `CardElevationShadow`（不透明阴影形 + 卡片同形挖内 + `compositingGroup` 隔离，
   浅色黑 / 深色白），投影形与卡片**同形**（同样上溢 `cardTopRise`）。
4. **`RightSidebarView.swift`**：删掉 body 上那圈 0.5pt 描边。
5. **`App.swift`**：`trafficLightNudge.y = −10`（推导写在注释里：距窗顶 19 → 容器 origin.y −1 → 基准 9 之差）。
6. **`TwoColumnLayoutPreview.swift`**：新增/更新「卡片吸顶（右栏开）」「卡片吸顶·仅中栏」两个形态，顶栏 padding 与生产同步。
7. **PiP**：无改动（`if !rightSidebar.isPipChat { macOSContentArea }` 已保证中栏不渲染 → 溢出消失）。

## 四、决定（已拍）

| # | 决定 |
|---|------|
| D1 | 「左侧的卡片」= **中栏（唯一左列）**；不恢复左侧栏（`mac-two-column-layout` D1 不变） |
| D2 | 卡片**要底色**：**白霜提亮 6%**（`Color.white`）—— Xcode 参考本身就是"有底的完整卡片"；早期"只把描边拐上去"已否，第一版用 `assistantText` 6% 真机偏暗沉也已否（见 §五） |
| D3 | 上边**停在窗口内** 6pt（不是压在窗顶）：四条边都看得见 |
| D4 | 顶栏其余部分位置不动，但整条顶栏内容下移到距窗顶 **12**（下边距仍 6） |
| D5 | 右栏**只去掉 0.5pt 描边**，内部分隔线/选中底不动 |
| D6 | PiP 不作额外处理（红绿灯 + 下拉"释放"= 中栏不渲染的自然结果） |
| D7 | 红绿灯下移到距窗顶 **19**（与顶栏内容同一行居中；2026-10-11 曾是 23，缩胶囊后随之改） |
| D8 | 右栏关着时**不额外处理**：中栏铺满整宽，顶栏按钮自然全被包住 |

## 五、已知后果 / 风险

- **红绿灯探出标题栏容器 1pt**（缩胶囊前是 5pt）：目标位置要求容器 `origin.y = −1`，而标题栏容器只有 32pt。已离屏验证（`cacheDisplay` 渲染
  完整不被裁 + `frameView.hitTest` 照常命中），属**有意依赖 AppKit 现状**；macOS 大版本改标题栏行为时要回来复验。
- **顶栏高 48 → 54 → 46**：凡写死旧值的**其它**文档都已同步 —— `window-background-photos.md`（§二 几何 + §六 验收清单）、
  `mac-two-column-layout/design.md`（§2.2 主陈述 + §10 后续变更）、`topbar-slot-system.md`（§八 红绿灯微移）；
  本文档的当前值看 §二、历史看 §八（文首那两行估算里的数值是当时的快照，按惯例不回改）。
- **顶栏里放进比 28pt 更高的元素**：`topBarHeight` 靠「顶栏 HStack 最高子视图 == `topBarCapsuleHeight`(28)」撑出来（会话下拉标签行与
  胶囊组各自显式 `.frame(height:)`；DEV 徽标更矮）。若将来有更高的子件，顶栏会变高，而 `cardTopRise` 仍按 46 算 →
  **卡片上边不再落在 6pt（静默漂移，不报错）**。改顶栏内容时要一眼扫到这条。
- **底色改过一次（真机）**：第一版用 `assistantText` 6%（"派生淡色"）——真机在照片底图上**整体偏暗沉**；
  改成 **`Color.white` 6%（白霜提亮）**，与 `AppBackdrop.frost` 同族、浅深主题都成立。
  浓度仍是渲染图 + 真机定的初值，偏亮/偏暗只改 `Layout.cardFillOpacity` 一个数。
- 深底图下右栏与底图的分界只剩内容自身的层次；真机看是否"糊在一起"，必要时再补一条极淡分界（本版不做）。
- 卡片底色的 `.padding(.top, -cardTopRise)` 是"画到视图边界之外"的写法：中栏到窗口顶之间**没有任何裁剪层**
  （已 grep 确认 `ContentView` 无 `clipped()`），若将来有人给这层加 `.clipped()`，上溢部分会被切掉。
- **投影性能**：卡片几乎铺满整窗，`blur(radius: 8)` 作用在大形状上 —— 拖拽缩放/滚动是否掉帧只能真机看；
  退路是"只右缘渐变"（`blur` 范围小得多，见 `/tmp/shadow_options.png` 第 4 格的做法）。
- **深色主题的白阴影强度**：深色档用白 10%（同全 app 卡片惯例），深色调下够不够/过不过只能真机看 —— 一个数（`CardElevationShadow` 里）。
- **挖内不能省**：去掉 `destinationOut` 那层，投影会透过 6% 白霜渗进卡片内部（等于又把里面压暗，正是要避免的）；
  去掉 `compositingGroup()` 则 `destinationOut` 会**在窗口底图上打洞**。

## 六、验收（真机）

1. 非 PiP：中栏四条边**都在窗口内**（上边 6pt、右下 6pt 圆角）；红绿灯与会话下拉在卡片里；无 1~2px 缝。
2. 红绿灯：与顶栏胶囊/按钮组**同一行居中**；可点、hover 有反应（探出容器不影响）；拖拽缩放/进出全屏后不跑位。
3. 右栏开 / 关各看一次：开着时卡片止于 seam、顶栏右侧按钮落在右栏（无描边）上；关着时卡片铺满整宽、**所有顶栏按钮都在卡片里**。
4. 缩放窗口、切深浅色、进出 PiP：上溢量恒定不抖；PiP 下无卡片边、红绿灯与下拉在底板上。
5. 顶栏左区交互正常（下拉可点、空白区双击仍缩放窗口）。
6. 底色浓度在深/浅两套主题 + 花哨底图下都可接受（不满意就调 `cardFillOpacity`）。

## 七、被否掉的中间方案（留个记录，别再走一遍）

1. **上边压窗顶（溢出 `cardTopRise` 直达 y=0）**：上边正好落在窗口上缘 → 被窗口圆角切，看起来"卡片超出屏幕"。**否**。
2. **只把描边拐上去、不加底色**：卡片"不完整"，不像 Xcode 那张有底的侧栏卡片。**否**。
3. **自绘 `CardOutline`（自定义 Shape，顶边不画）**：是为方案 1 造的形状，方案 1 否掉后一并删除；
   现在回到 `RoundedRectangle(12).strokeBorder`，与全 app 其它卡片同源（也顺手消掉"`stroke` 居中 vs `strokeBorder`
   内侧"那 0.25pt 差异）。
4. **红绿灯留在距窗顶 13**：与顶栏内容中心差 10pt，不像 Xcode 那种"同一行居中"。**否**（改 23，后又随胶囊缩到 19）。
5. **底色用 `assistantText`"派生淡色"6%（第一版）**：真机在照片底图上**整体偏暗沉** —— 暗色 tint 压在亮底图上必然如此。**否**，
   改成 `Color.white` 6%（白霜提亮，与 `AppBackdrop.frost` 同族、浅深主题都成立）。
6. **不给卡片投影 / 直接对半透明卡片用 `.shadow`**：前者平（Xcode 那种"高一层"出不来）；后者投影会**渗进卡片内部**
   （等于把里面又压暗）。**否** —— 用 `CardElevationShadow`：不透明阴影形 + 卡片同形挖内（浅色黑 10%/`blur 8`/`y 3`，深色白）。

## 八、后续变更

- **2026-10-12 · 顶栏胶囊 36 → 28，红绿灯随之到距窗顶 19**
  - 决定：顶栏胶囊（= 按钮组外壳）比 Xcode 胖（36 vs 约 20），红绿灯被顶得偏下。收 `topBarCapsuleVPadding` 5 → **1**
    （胶囊贴着 26pt 按钮盒），`topBarTopGap` 12 不动。选"只收内边距"这条是为了**不动 `topBarIconSize`** —— 顶栏降级的宽度账、
    3 个宽度测试、`mac-topbar-degrade` 的宽度表全部无需重算（候选对照见 `/tmp/topbar_heights.png`）。
  - 派生结果：胶囊 **28** / `topBarHeight` **46** / `cardTopRise` **40** / 红绿灯距窗顶 **19**（nudge.y **−10**，探出标题栏容器由 5pt 降到 **1pt**）；
    ＋按钮、会话下拉、DEV 徽标、顶栏竖线（`UnresizableDivider`）均取 `topBarCapsuleHeight`，自动跟随。
  - 影响文件：`LayoutConstants.swift`（`topBarCapsuleVPadding` + 两处注释）、`xHelperMac/App.swift`（注释）、
    `xHelperMac/TwoColumnLayoutPreview.swift`（注释）、`../mac-apps/window-background-photos.md`（§二/§六）、
    `../mac-two-column-layout/design.md`（§2.2 + §10）、`../mac-apps/topbar-slot-system.md`（§八）。
  - 未做（记一笔）：`Layout.topBarSwitcherChromeWidth` 里"＋按钮 26"是既有写死值，而视图里 ＋ 取 `topBarCapsuleHeight`
    （36 → 28）→ 宽度模型对该按钮一直少算（旧 10、现 2 = 28 − 26）。本次不动它（缩胶囊只会让误差变小），要修需连带重算 `mac-topbar-degrade` 的宽度账。
- **2026-10-12 · 明确「右栏开着时按钮不落在卡片上」这条不变量**
  - 背景：`../mac-topbar-degrade/design.md` 原来只按窗口内容宽选档，中心槽组一显形就整簇压到卡片右上半区
    （`docs/bugs/034`）。已在降级侧加闸（`trailingWidth ≤ 卡片右侧可用宽 − margin`），本文件把不变量写死，
    免得下次又按「窗口够宽就显形」改回去。
  - 边界：右栏关着（卡片铺满整宽）/ PiP（整窗就是右栏）时中栏不占位 → 不受此闸约束（那段「所有按钮都落在卡片里」仍成立）。
  - 影响文件：`../mac-topbar-degrade/design.md`（§2.1 表、§3.3 两张表 + 设计要点 4、§四 D6、§六、§七）、
    `Shared/Views/{TopBarLayout,TopBarView,ContentView}.swift`（细节见 `docs/bugs/034`）。
