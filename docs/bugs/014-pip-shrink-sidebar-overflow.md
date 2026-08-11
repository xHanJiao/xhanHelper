# PiP 缩窗左栏溢出 —— 同容器内各部分最小宽不一致（顶栏拖累底栏）

## 问题表现

macOS 客户端 PiP（聊天画中画）模式下，窗口最大化后进入 PiP，拖动窗口左缘缩窗：窗口能一路缩到理论最小宽（661），但**最左侧卡片左边缘被挤出窗口外约 80px**，且左栏**不会自动收起**。

3 栏模式（左+中+右）无此问题：缩窗时左栏先正常收起，到阈值后窗口不能再缩小。

## 引发路径

1. 最大化窗口（内容宽 1440）→ 进 PiP（左栏 276 展开，右栏浏览器）
2. 拖窗口左缘缩窗
3. 窗口被 `windowWillResize` 钳到 `requiredMinContentWidth = 661`（PiP 理论最小）
4. 但 SwiftUI 内容不肯缩到 661 以下 → 813 宽的内容被**居中**进 661 窗口 → 左栏 `gx=-65` 出窗口

## 根因：同一容器内各部分最小宽不一致

三栏不是上下独立的——顶栏（TopBarView）和底栏（HStack）叠在**同一个 VStack** 里，宽度必须一致，**VStack 宽度 = 最宽子视图的宽度**。

| 部分 | 内在最小宽 | 说明 |
|------|-----------|------|
| 底栏 HStack | **661** | `cardGap×2 + 左栏288 + leftSeam + 右栏340` |
| 顶栏 TopBarView | **813** | `navLeadingOffset(=左栏+34=310) + 浏览器按钮槽位` |
| 整个 VStack | **813** | 取最宽子视图，顶栏拖累全局 |

窗口最小宽守卫 `pipMinContentWidth` 按**底栏 661** 推导，**漏算了顶栏 813**：

1. 窗口被允许缩到 661 < 813
2. SwiftUI 内容死守 813 不缩（`geo.size.width` 冻结在 813）
3. 813 内容居中进 661 窗口 → 左右各溢出 76px → 左栏出窗口
4. `geo.size.width` 冻结 → `onChange(of: geo.size.width)` 停止触发 → **自动收起和右栏 clamp 全部失效** → 左栏永不收起

### 为什么 3 栏没这问题

| 模式 | 窗口 min | 收起阈值 | 关系 |
|------|---------|---------|------|
| 3 栏 | 776 | `sidebar+736`=1012 | 1012 > 813（底线）→ 冻结前先收左栏 → 底线降到 ~575 → 窗口缩到 776 无溢出 |
| PiP（修复前） | 661 | `sidebar+385`=661 | 661 < 813（底线）→ 冻结先发生 → 收起永不触发 → 死锁 |

**本质：PiP 的 min/阈值必须对齐顶栏驱动的真实内容底线，而不是底栏 HStack 的常数。**

## 误判记录（第一版修复为何不生效）

### 误判 1：测量源拿错了尺子

第一版修复在 `windowWillResize` 里读 `sender.contentMinSize.width` 当内容真实底线，以为
`max(661, contentMinSize)` 会把窗口钳到 813。**实际 contentMinSize = 400，守卫仍是 661，行为完全没变。**

原因：`.windowResizability(.contentMinSize)` 用的是**根视图声明的 min**（`ContentView.frame(minWidth: 400)`），
内部 VStack 的 813 不会向上传播到 contentMinSize。

**探针确认**（`[Src]` 日志）——AppKit 层所有候选全部失败：

```
[Src] proposed=579 requiredMin=661 contentMinSize=400 minSize=400 fitting=0 intrinsic=-1
```

| 候选 | 值 | 结论 |
|------|-----|------|
| `contentMinSize` / `minSize` | 400 | 根视图声明，不是内容底线 |
| `fittingSize` | 0 | 未计算 |
| `intrinsicContentSize` | **-1** | `NSViewNoIntrinsicMetric`——NSHostingView 对 AppKit 报「弹性」 |

**结论：813 只存在于 SwiftUI 布局引擎内部（`geo.size.width` 冻结值），AppKit 层（windowWillResize）根本量不到它。测量必须在 SwiftUI 内做。**

### 误判 2：忽略冻结引发的死锁

第一版还犯了第二个错：直接测 `contentMinSize` 不生效后，草拟的替代方案（`didResize` 冻结检测）
最初没考虑**收起后测量值粘滞**的死锁：

```
1. PiP 展开左栏 276 → 冻结在 813 → measured = 813 → guard = 813
2. 窗口停在 813，收起条件触发 → 左栏收起为 38          ✅
3. 收起后顶栏缩到 575，但 geo 冻结在 813 没再变 → measured 还是 813
4. guard 还是 813 → 窗口卡死在 813 → 收起后的真实底线 575 永远测不到
```

**解法：每次左栏宽度/模式变化时复位 `measuredContentMinWidth = 0`**，让窗口能继续缩、
到新底线再重测。复位点：`sidebarWidth` / `isPipChat` / `isOpen` 三个 onChange。

## 最终方案（SwiftUI 内冻结检测，单一来源）

### 核心机制：检测 geo 冻结，把内容底线交给窗口守卫

`geo.size.width`（SwiftUI 内容布局宽）在窗口缩到内容底线后**冻结在底线**（813），
之后窗口继续缩、geo 不再变。冻结后 `onChange(geo.size.width)` 不再触发，但
`NSWindow.didResizeNotification`（窗口帧层）**每帧都触发**——检测点必须放在这里。

### 核心改动（2 个文件）

**App.swift `windowWillResize`** — 守卫取 `max(理论 min, 内容实测 min)`：

```swift
static var measuredContentMinWidth: CGFloat = 0   // 由 ContentView 实测写入
static var pipExpandedFloor: CGFloat = 0          // PiP 收起瞬间的展开态底线

func windowWillResize(_ sender: NSWindow, to frameSize: NSSize) -> NSSize {
    ...
    let realMin = max(Self.requiredMinContentWidth, Self.measuredContentMinWidth)
    let minFrameWidth = realMin + chrome
    return NSSize(width: max(frameSize.width, minFrameWidth), height: frameSize.height)
}
```

**ContentView** — 四处：

1. `lastGeoWidth` @State：记录最近一次 `geo.size.width`（冻结后即内容底线）

2. `onChange(of: geo.size.width)`：记录 lastGeo；每次 geo 变化复位实测值（旧值随
   左栏/顶栏状态失效）；3 栏/关闭模式保持原阈值逻辑；PiP 恢复下限 =
   `max(storedSidebar + overhead, pipExpandedFloor)` 防「窗口还没大到容下展开内容就恢复」

3. `.onReceive(NSWindow.didResizeNotification)` — 核心检测点：
   ```swift
   // ① 溢出检测：geo 冻结值 > 窗口实际内容宽 → lastGeoWidth 即内容真实底线
   if lastGeoWidth > contentW + 1 {
       AppDelegate.measuredContentMinWidth = lastGeoWidth
   }
   // ② PiP 缩窗至内容底线：先收左栏 → 顶栏 navLeadingOffset 缩小 → 底线降下来
   //    （如 813→575）→ 窗口可继续缩，与 3 栏「先收再缩」行为对齐
   if rightSidebar.isPipChat, lastGeoWidth > contentW + 1, sidebarWidth > Layout.sidebarMinWidth {
       AppDelegate.pipExpandedFloor = lastGeoWidth
       sidebarWidth = Layout.sidebarMinWidth
       sidebarAutoCollapsed = true
   }
   ```

4. 三处复位（`sidebarWidth` / `isPipChat` / `isOpen` 变化时）：
   `measuredContentMinWidth = 0`（+ `pipExpandedFloor = 0`）——内容底线随
   顶栏按钮组/左栏宽度变化，旧实测值必须作废重新测量

### 完整周期推演

```
PiP，左栏=276，measured=0，guard=661

缩窗 1440→813：geo 跟踪，无溢出，measured 保持 0（onChange 每次复位）

缩窗 813→811：geo 冻在 813，lastGeo=813
  didResize: 813 > 811+1 → 溢出！
    → measured = 813，guard = 813
    → PiP 且左栏展开 → 记录 pipExpandedFloor=813，左栏收起为 38
  → onChange(sidebarWidth) 复位 measured=0 → guard=423

缩窗 811→575：geo 跟踪（窗口 > 新底线 575），无溢出

缩窗 575→573：geo 冻在 575
  didResize: 575 > 573+1 → 溢出！→ measured = 575 → guard = 575

窗口稳定在 575，左栏已收，卡片不出界        ✅

拉宽恢复：窗口 ≥ 813（pipExpandedFloor）→ 左栏恢复展开
```

瞬态代价：检测滞后一帧，窗口最多过冲 ~2px 再回弹，拖拽中不可感知。

## 定位过程（探针数据）

`sideFrame gx` / `hstack allocW` / `topbar w` / `vstack w` 四个 GeometryReader 探针 + `windowWillResize` 日志：

```
[PiP] hstack: allocW=813 geoW=813 need=661   ← HStack 冻结在 813（need 才是 661）
[PiP] sideFrame gx=-65 seam=11 w=288          ← 左栏 global x 变负，seam 内位置恒定 = 内容被居中
[PiP] [WindowResize] CLAMPED proposed=647 -> 661 contentW=661
[Src] proposed=579 requiredMin=661 contentMinSize=400 minSize=400 fitting=0 intrinsic=-1  ← AppKit 量不到底线
```

- `(661 − 813) / 2 + 11 = -65` ✓ 与 `gx=-65` 精确吻合（813 内容居中溢出）
- `hstack need=661`（底栏纯布局）vs `allocW=813`（实际分配）→ 差额来自 VStack 内另一条路径
- `topbar w` 与 `vstack w` 同步冻结在 813 → 顶栏才是 813 的来源
- `[Src]` 探针证明 AppKit 层无测量源 → 测量必须下沉到 SwiftUI 内

## 经验教训

### 同一容器内，各部分的最小/最大尺度必须一致

VStack 宽度 = 最宽子视图宽度。顶栏和底栏共享同一宽度，**一个部分的最小宽就是整个容器的最小宽**。算窗口最小宽时，不能只算"关心的那一栏"（底栏 HStack），要算**整个内容的最小值**。

### 与 013 的关联：约束单一来源原则

- **013（横向）**：容器的「允许值」（分隔条 maxWidth/窗口最小宽）与内容的「需求值」必须一致 → 我给了 276，内容只能放 200 → 溢出
- **014（纵向/同容器）**：同一容器内各部分尺度必须一致 → 顶栏要 813，底栏只要 661 → 溢出

统一认知：**一个约束只有一个来源，所有消费它的地方从同一处取值**。013 的约束是「窗口能放多宽」，014 的约束是「整个内容（含顶栏）最小要多宽」。

### 测量要在能拿到数据的地方做

- 内容真实底线（813）只存在于 SwiftUI 布局引擎内部，AppKit 的 `contentMinSize`/`fittingSize`/`intrinsicContentSize` 全部拿不到（`-1` = 弹性）
- 所以测量必须下沉到 SwiftUI 侧（`geo.size.width` 冻结值），再通过静态变量桥接给 AppKit 守卫
- **写代码前先确认「数据在哪一层」，否则会在拿不到数据的层瞎找尺子**

### AppKit 层 vs SwiftUI 层：为什么是两个层、数据为什么不通

macOS 上 SwiftUI app 实际跑在两个层上：

| 层 | 角色 | 类比 |
|----|------|------|
| **AppKit** | 窗口的物理矩形：frame 尺寸、屏幕位置、边缘拖拽手柄。`windowWillResize` 在此层被调用（AppKit 问「用户想把窗框拖到多宽」） | 房子的墙壁/窗框 |
| **SwiftUI** | 内容布局：VStack/HStack/TopBarView/SidebarView 怎么摆。`GeometryReader`（`geo.size.width`）在此层报告「家具至少要占多宽」 | 房子里的家具 |

关键点：

- **SwiftUI 不独立存在**——它嵌在 AppKit 的一个特殊 NSView `NSHostingView` 里，这是两层之间的桥
- AppKit 问桥「你最小要多宽？」桥回答 `intrinsicContentSize = -1`（`NSViewNoIntrinsicMetric`）——「我弹性，给多宽摆多宽」。因为 SwiftUI 内容里有 greedy 的 GeometryReader，SwiftUI 对桥的请求也报弹性
- **所以 AppKit 只看到窗框尺寸（661），永远问不出家具的 813**。813 只在 SwiftUI 布局引擎内部算得出（`geo.size.width` 冻结值）

代码里的体现：

```
windowWillResize（AppKit 层）想量内容最小宽：
  sender.contentMinSize.width                → 400（窗框声明的最小宽度，根视图 minWidth）
  sender.contentView?.intrinsicContentSize   → -1（桥说「我弹性」）
  → 守卫只能 = max(661, 0) = 661            ← 量不到 813

geo.size.width（SwiftUI 层）：
  → 813（家具真实最小占位）
  → 窗口 661 < 813 → 家具超出墙壁 → 居中 → 左栏出墙
```

正确做法：**SwiftUI 层量出家具真实宽度 → 写进共享静态变量（`AppDelegate.measuredContentMinWidth`）→ AppKit 层读它当守卫**。两层不直接通话，通过静态变量桥接。


### 布点教训

`onChange(of: geo.size.width)` 在内容冻结后（宽度不再变化）会**停止触发**——冻结引发的次级失效（自动收起/钳制）不能靠它兜底。真正的检测必须在 `windowWillResize` / `NSWindow.didResizeNotification`（窗口帧层，始终触发）。

### 死锁教训：守卫与测量互相依赖时要考虑状态粘滞

守卫用旧状态的值锁死窗口 → 新状态永远测不到 → 死锁。凡是「测量值驱动守卫、守卫限制测量条件」的循环，都要在**状态变化的每个入口**复位测量值。
