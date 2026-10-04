# 右侧栏切换功能时宽度跳变（浏览器自动加宽）

## 问题表现

macOS 客户端，从浏览器切到「文档」（或账单/记忆等浏览面板）时，右侧栏宽度会变。

## 根因

来自「浏览器聚焦」特性（`middleMinimized`）：进入浏览器时把中间聊天区缩到最小、右栏自动铺满腾出的空间，离开浏览器时恢复进入前的宽度。切换功能时 `rightSidebar.width` 在两条值之间跳。

### 触发链

**进入浏览器**（顶栏地球键 / ⌘4）：

1. `TopBarView.swift:314` → `BrowserToolProvider.enterBrowser()`
2. `BrowserToolProvider.swift:2150` → `rs.middleMinimized = true`
3. `ContentView.swift` `onChange(of: middleMinimized)` → `enterBrowserFocusEffects()`
   - 存 `preFocusSidebarWidth = rightSidebar.width`
   - `rightSidebar.width = rightSidebarMaxAvailable`（铺满可用宽，上限 4000）

**切到文档**（顶栏文档键 / ⌘1 / 内部文档内链）：

1. `TopBarView.swift:384` `openBrowsePanel(.documents)` → `showBrowsePanel()` → `activeTabId = nil`
2. `ContentView.swift` `onChange(of: activeTabId)` → `middleMinimized = false`
3. `onChange(of: middleMinimized)` → `exitBrowserFocusEffects()` → `rightSidebar.width = min(preFocusSidebarWidth, rightSidebarMaxAvailable)`

右栏实际渲染宽是 `ContentView.swift` 的 `.frame(width: rightSidebar.width)`，`width` 一变画面即跳。

### 佐证

- 从浏览面板网格的「打开浏览器」走 `openBlankTab()`（`BrowsePanelView.swift:391`），不设 `middleMinimized`，那条路进浏览器宽度不变——同一功能两个入口表现不一致。
- 该行为从未进入设计文档（只在代码与注释里），PiP 的自动加宽是另一条独立路径（`isPipChat`）。

## 最终方案

**彻底移除「浏览器聚焦自动加宽」**（用户拍板：切换功能时右栏宽度不变）。宽度此后只由手动拖拽或 PiP 改变。

### 核心改动（3 个文件）

| 文件 | 改动 |
|------|------|
| `BrowserToolProvider.swift` | `enterBrowser()` 删去 `if !rs.isPipChat { rs.middleMinimized = true }` |
| `RightSidebarManager.swift` | 删去 `middleMinimized` 属性 |
| `ContentView.swift` | 删去 `preFocusSidebarWidth`、`enterBrowserFocusEffects` / `refillBrowserFocus` / `exitBrowserFocusEffects`、`onChange(of: middleMinimized)`，以及各处对它复位/加宽上限（`rightSidebarMaxAvailable` 上限恢复为「仅 PiP 放宽到 4000」，`ResizableDivider` 的 `onDragStart` 复位、侧栏关闭/`activeTabId` 为 nil 的复位） |

顺带修掉「网格进浏览器不加宽、顶栏地球键加宽」的入口不一致。

## 验证

- `xcrun swiftc -typecheck`（macOS 26.3 真实设置）通过，0 error。
- 真机（用户在 Xcode）：浏览器 ↔ 文档/账单等来回切，右栏宽度不再跳；手动拖拽调宽、PiP 最大化仍正常。

## 经验教训

**状态里专门为某个界面临时改布局尺寸的字段（如 `middleMinimized`）是宽度跳变的常见来源**。这类「进入 A 就自动放大、离开再恢复」的隐式尺寸切换，若没有对应的设计文档与一致入口，很容易被当成 bug。今后同类「自动加宽」需求，先确认用户是否接受宽度随功能变化；PiP 那种由用户显式触发的最大化是合理形态，隐式自动加宽则默认不做。
