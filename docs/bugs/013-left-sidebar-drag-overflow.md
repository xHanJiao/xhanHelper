# 左侧栏拖拽/缩窗溢出 —— 容器最大宽与分隔条最大宽不一致

## 问题表现

macOS 客户端窄窗口下，左侧栏分隔条可拖拽到 276px（`Layout.sidebarMaxWidth`），超过 HStack 实际容纳能力，导致中间栏被压缩到 340px 以下，「内容变宽但容器没变宽」。同时也可能通过缩窗触发 HStack 瞬时压缩侧栏，视觉上闪烁/空白条。

## 引发路径

1. `leftSidebarMaxAvailable`（分隔条的 maxWidth）在右栏关闭时**硬返回常量 276**，不检查窗口实际可用空间
2. `baseMinWidth` 用 `sidebarMinWidth=38` 静态计算（423），与侧栏实际宽度（如 149/276）脱节
3. `toggleLeftSidebar` 恢复时钳制到 `sidebarMaxWidth=276`，同样不检查窗口空间

三处各写各的约束值，没有一个统一的源头，导致「分隔条允许 276、窗口只能放 200」的矛盾。

## 走过的弯路

### 弯路 1：侧栏外层刚性化（`.frame(width: sidebarWidth)`）

以为把外层从柔性 `minWidth: 38` 改成刚性 `width: sidebarWidth` 能「更彻底防压缩」。

**为什么弯：** 刚性 frame 改变了 HStack 对侧栏的宽度分配方式，引发了窄窗口点击智能体闪烁/空白条——HStack 在 tab 切换的瞬时重布局中，无法压缩刚性侧栏，反而推给中栏后触发连锁布局异常。

### 弯路 2：SidebarView 加 GeometryReader + 防抖逻辑

为应对「外层被压缩但 @State 不变」的场景，在 SidebarView body 包了一层 GeometryReader，用实际分配宽度（而非 @State）判断 collapsed/expanded，并加 100ms 防抖过滤瞬态。

**为什么弯：** 这引入了**新的 bug——图标偏右**。GeometryReader 的 greedy 布局特性改变了 ScrollView 内部的宽度提案，使 collapsedContent 的内层 VStack 收缩到内容固有宽度（46px），但 ScrollView 视口只有 38px，内容左对齐 → 图标偏右 4px。

同时，`.task(id:)` 里读 `GeometryProxy.size` 是在 `async` 挂起后读——此时代理已失效，读回垃圾值 0，导致 `debouncedCollapsed` 随机翻转。

### 弯路 3：collapsedContent 加 `.frame(maxWidth:.infinity)` 修正偏右

**为什么弯：** 在 ScrollView 内层 VStack 加 maxWidth infinity 试图撑满视口。但此时视口已被 GeometryReader 扰乱，且 modifier 顺序（frame 在 padding 前）进一步复杂化，最终未生效且引入新的布局冲突。

**结论**：所有弯路都源于在侧栏外层做了多余的「加固」（刚性 frame），然后一路修自己引入的 bug。根因其实只有一处：`leftSidebarMaxAvailable` 算错了。

## 最终方案

### 核心改动（1 个文件）

**ContentView.swift** 4 处改动（提交 `f28e1f5`）：

| 改动 | 为什么 |
|---|---|
| `leftSidebarMaxAvailable` 右栏关闭时不再返回常量 276，改为按窗口实时计算: `windowWidth - cardGap - hpad - seam - middleMin` | 让分隔条 maxWidth 始终反映 HStack 实际容纳能力 |
| `baseMinWidth` 从静态（`sidebarMinWidth=38`）改为动态跟随 `sidebarWidth`: `min(max(sidebarWidth, 38), leftSidebarMaxAvailable)` | 窗口最小宽与侧栏实际宽度同步，缩窗守卫不会失效 |
| `toggleLeftSidebar` 恢复时 max 从 `sidebarMaxWidth` 改为 `leftSidebarMaxAvailable` | Cmd+B 展开不会超出可用空间 |
| `updateWindowMinWidth()` 在 `onChange(of: sidebarWidth)` 中调用 | 侧栏每次变宽时，窗口最小宽守卫实时更新 |

**SidebarView.swift** 不改动（与 HEAD 完全一致）。

### 关键决策

**所有宽度约束归一到 `leftSidebarMaxAvailable` 一个源头。**

- 分隔条 `maxWidth` = `leftSidebarMaxAvailable`
- Cmd+B 恢复上限 = `leftSidebarMaxAvailable`
- 窗口最小宽 = `baseMinWidth` = `sidebar + overhead`，其中 sidebar 已钳制到 `leftSidebarMaxAvailable`

**不画蛇添足。** 侧栏外层保持原样 `minWidth: 38`（柔性），不需要刚性 frame。因为 `leftSidebarMaxAvailable` 已经从根本上杜绝了「分隔条可拖超出可用空间」的矛盾。

## 经验教训

### 问题本质：容器最大宽与内容最大宽各写各的

| 约束路径 | 旧值来源 | 新值来源 |
|---|---|---|
| 分隔条 maxWidth | 常量 276 | `leftSidebarMaxAvailable`（窗口实时算） |
| 窗口最小宽 | 静态 423（用 38 算） | `baseMinWidth`（用实际 sidebarWidth 算） |
| Cmd+B 上限 | 常量 276 | `leftSidebarMaxAvailable` |

三个看似独立的数值，本质上是同一个物理约束的三种表达——但旧代码给了三种不同的值。

### 一般解法

**1. 所有依赖同一约束的地方，从同一个计算公式出（单一来源原则）。**

不要各写各的。分隔条 maxWidth、窗口最小宽、Cmd+B 上限——全调用同一个函数。改它就等于改全部。

**2. 让非法状态不可到达——在每个入口做校验（写时校验，而非布局时容错）。**

拖拽分隔条、Cmd+B、窗口缩放——每条改变 `sidebarWidth` 的路径，写入前都钳制到可用空间。不当做「布局渲染时再修」，在写入时就拒绝越界值。

**3. 修 bug 时不做多余的事（最小修复原则）。**

溢出根因只是 `leftSidebarMaxAvailable` 算错了——改它一处就行。刚性 frame、GeometryReader 全部是画蛇添足，每多一个约束就多一个出错点。

**4. 读 GeometryProxy 只在同步阶段（不要跨 async 边界）。**

`GeometryProxy` 只在 body 同步求值时有效。捕获进 `Task`、`await` 后再读 `geo.size`，读到的是失效代理的垃圾值（稳定 0）。若需异步 debounce，必须在同步回调（如 `onChange`）中把值捕获到局部变量，任务只读捕获值。
