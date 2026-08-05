# 右侧栏分隔线拖拽调宽失效

## 问题表现

macOS 客户端右侧栏打开浏览器后，分隔线（右 seam 上的拖拽手柄）无法调整右侧栏的宽度。右侧栏始终固定在 340px（最小宽），分隔线拖拽虽然写入了 `rightSidebar.width` 的 `@Published` 值，但视觉上卡片宽度不变。

同时在缩窗过程中，有时左边栏会**被推出窗口可视区外**（看似“消失”），HStack 内容总宽可能超过窗口宽但未裁剪。

## 引发路径

1. HStack 三栏布局无显式宽上限，内容总宽可超过容器宽
2. 右栏内部 `frame(width: max(manager.width, 340))` 强制占用固定宽，PiP 时 `enterPipEffects` 硬设 4000px——远超窗口可容纳
3. 分隔线 `maxWidth` 依赖 `windowWidth`（@State 值，在 `onChange` 中更新、滞后一帧），窄窗时算出 `maxWidth == minWidth = 340` → `isResizable = false` → 手势被关
4. `layoutPriority` 控制的是**缩窗顺序**（谁先放弃空间），但不控制**扩窗顺序**——宽窗时中栏 `maxWidth: .infinity` 先吞掉所有剩余空间，右栏 `idealWidth` 形同虚设

## 走过的弯路

### 弯路 1：用 `effectiveSidebarWidth` 桥接内层 frame（`09fdb51`）

在 body 内实时计算 `effectiveSidebarWidth = min(sidebarWidth, availableSpace)`，构造 `sidebarBinding(get: effectiveSidebarWidth, set: { sidebarWidth = $0 })` 传给 SidebarView。目的是让 SidebarView 内部 frame 和 HStack 实际分配的空间一致。

**为什么弯：** `effectiveSidebarWidth` 使侧栏连续等比例收缩（200→180→150→...→38），但 `isCollapsed` 阈值是 120。中间值（100–120）时侧栏切到图标模式但仍占 100px，视觉怪异。需要加 snap 阈值强行跳到 38，徒增复杂度。

**回头看**：如果窗口硬最小约束先到位（`baseMinWidth=423`），侧栏永远不会被压到低于 `sidebarMinWidth=38`，没必要用 `effectiveSidebarWidth` 做桥接。

### 弯路 2：试图只用 `layoutPriority` 控制三栏弹性（`09fdb51`）

三栏分别设 `layoutPriority(3, 2, 1)`，希望缩窗时中栏(1)先收、扩窗时右栏(2)先展。

**为什么弯：** SwiftUI `layoutPriority` 只管**空间不足时谁先缩**。**空间富余时**，HStack 按声明顺序分剩余空间——中栏在右栏之前，中栏带着 `maxWidth: .infinity` 先吞掉所有剩余空间，右栏 `idealWidth` 根本不起作用。日志证实：`ide=766 actual=340`。

### 弯路 3：外层 frame 与内层 frame 冲突（`183bbe3`）

在 ContentView 给 RightSidebarView 加了外层的 `.frame(minWidth:idealWidth:maxWidth:)`，同时 RightSidebarView 内部也有自己的 `.frame(minWidth:idealWidth:maxWidth:.infinity)`。

**为什么弯：** 两层 frame 互相覆盖。SwiftUI 报了 `Contradictory frame constraints`。外层 frame 抹掉了内层的 `idealWidth`，HStack 不知道右栏想要多大宽，给最小值了事。去外层、只留内层即可——但 layoutPriority 的根缺陷仍在。

### 弯路 4：用 `effectiveSidebarWidth` 的复杂 snap 逻辑（中间态跳变）

在 `effectiveSidebarWidth` 上加 snap：`≤ 120 → 38`，`> 120 → 按比例`。出现 122↔38 跳变。不是问题本身，但说明这套方案复杂度已失控。

## 最终方案

### 核心改动（3 个文件，几十行，3 次提交）

| 提交 | 改了什么 | 为什么 |
|---|---|---|
| `09fdb51` | 三栏 `layoutPriority(3>2>1)`、右栏内层改为弹性 frame、`baseMinWidth=423`、卡片 `clipShape` 兜底 | 建立缩窗顺序和硬最小约束框架 |
| `183bbe3` | 侧栏自动收起/恢复（`sidebarAutoCollapsed`）、PiP 自动扩窗、右栏分隔线 `maxWidth` 改用 `geo.size.width` 实时算 | 解耦侧栏塌缩、修分隔线 `isResizable` 误判 |
| `b3af895` | **右栏非 PiP 用 `.frame(width: rightSidebar.width)` 硬设宽**，中栏 `idealWidth` 锁定 340 | 根因修复——绕过 layoutPriority 扩容缺陷 |

### 最终布局架构

```
┌──────────────────────────────────────────────┐
│  HStack (spacing:0, constrained by VStack)   │
│                                              │
│  ┌──────┐ ┌─┐ ┌─────────────┐ ┌─┐ ┌───────┐ │
│  │ 侧栏  │ │缝│ │   中栏      │ │缝│ │ 右栏  │ │
│  │ P=3  │ │ │ │ P=1        │ │ │ │ frame │ │
│  │ min:38│ │ │ │ ideal:340  │ │ │ │ width │ │
│  │ max:  │ │ │ │ max:.inf   │ │ │ │ 明确 │ │
│  │ 276   │ │ │ │            │ │ │ │ 设宽  │ │
│  └──────┘ └─┘ └─────────────┘ └─┘ └───────┘ │
│                                              │
│  缩窗：中栏(1)先收 → 侧栏(3)最后收            │
│  扩窗/拖拽：右栏 frame(width) 强制占用         │
│  PiP：中栏隐藏，右栏 nil(width) → 自动撑满     │
└──────────────────────────────────────────────┘
```

### 关键决策

**非 PiP 时右栏用 `.frame(width: rightSidebar.width)` 硬设宽，PiP 时不设（nil）。**

理由：
- `layoutPriority` 无法控制 HStack 的扩容顺序（声明顺序决定）
- 显式 `.frame(width:)` 强制 HStack 分配对应宽度，不再依赖 priority
- PiP 时传 nil → 内层 `maxWidth: .infinity` 生效 → 自动撑满窗口

**中栏 `idealWidth` 锁定为 `Layout.middleMinWidth(340)`。**

理由：
- 中栏 `maxWidth: .infinity` 会让其上报的 ideal 无限大
- 显式设 `idealWidth: 340` 收口中栏的“理想宽”，避免它在扩窗时抢走右栏需要的空间
- `maxWidth: .infinity` 保留——中栏仍能填满左栏伸缩后剩余的宽度

**侧栏 `layoutPriority(3)` 保留。**

理由：
- `layoutPriority` 在缩窗时仍然有效——中栏(1)先收到 340、侧栏(3)最后收
- 侧栏是 `.frame(minWidth: 38)` + SidebarView 内部自控宽，不需像右栏那样硬设

**分隔线 `maxWidth` 改用 body 内 `geo.size.width` 实时算。**

理由：
- 原 `rightSidebarMaxAvailable` 依赖 `windowWidth`（@State，`onChange` 更新滞后一帧）
- 窄窗时 `windowWidth` 初始值 800 算出 `maxWidth = 340 = minWidth` → `isResizable = false`
- 用 `geo.size.width` 实时算消除滞后

**侧栏自动收起/恢复（`sidebarAutoCollapsed`）。**

理由：
- 窗口硬最小按全收起算（38+340+340=776），侧栏展开时不能缩太窄
- `onChange(of: geo.size.width)` 检测窗口 < 当前侧栏最小内容宽时自动收侧栏到 38
- 窗口拉宽 ≥ 偏好侧栏最小内容宽时自动恢复

## 经验教训

1. **`layoutPriority` 是单向的**——只控制“谁先缩”，不控制“谁先扩”。扩窗分配按 HStack 声明顺序。
2. **`frame(maxWidth:)` 会抹掉 `idealWidth`**——只用 `maxWidth` 参数时，SwiftUI 把 `idealWidth` 设为 nil，子视图的 intrinsic size 被丢弃。
3. **`windowWidth` @State 滞后一帧**——用 `onChange(of: geo.size.width)` 更新 `@State`，但 body 已按旧值渲染一遍。分隔线 `maxWidth` 之类需要实时值的地方直接用 `geo.size.width`。
4. **Double-frame 嵌套是无用功**——RightSidebarView 内层 frame 已经很弹，外层 frame 反而拦截了 idealWidth 导致矛盾约束。核心组件应自控宽度，外层只在必要时覆盖硬值。
5. **复杂桥接方案往往是设计漏洞的信号**——`effectiveSidebarWidth` 连续变化 + snap 阈值 + sidebarBinding 等等，都是为了绕过应该用窗口最小宽杜绝的基础问题。事后看，`baseMinWidth` 修好 + 侧栏自动收起就够了。
