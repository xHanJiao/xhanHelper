# Swift 6 并发警告修复记录

> 2026-07-18 · 修复 MCP 模块 70+ 条 Swift 6 并发警告的完整过程

## 背景

`client-side-mcp.md` 落地后，MCP 模块的 Swift 代码在 Swift 6 语言模式下出现大量「Main actor-isolated X cannot be used from a nonisolated context」警告。这些代码在 Swift 5 下编译通过且运行正常，问题根源是 Swift 6 更激进的 `@MainActor` 推断策略。

---

## 为什么会告警

### 火花：`LogBuffer` 是 `ObservableObject`

Apple 在 iOS 17 / Swift 5.9 时期将 `ObservableObject` 协议标记为 `@MainActor`——因为它用于驱动 SwiftUI 视图更新，天然必须在主线程。`LogBuffer` 继承了 `ObservableObject`（为了在诊断 UI 里展示日志），因此被推断为 `@MainActor`。

### 传染：Swift 6 的全有或全无推断

Swift 6 的推断不只看「你标了什么」，更看「你用了什么 API」。一旦某个 API 被推断为 `@MainActor`，编译器会一路往上追溯整个调用链：

```
LogBuffer (ObservableObject → @MainActor)
  ├─→ 传染 MCPLog（调了 LogBuffer）
  ├─→ 传染 MCPError（LocalizedError，同一文件）
  ├─→ 传染 JSONRPCResponse / MCPToolDef / JSONRPC（同一文件）
  ├─→ 传染 MCPActivityTracker（调了 MCPLog）
  └─→ 传染 MCPServer（调了 MCPLog）
        └─→ 传染 MCPManager 中所有使用 MCPServer 的代码
```

### 冲突：运行位置 vs 编译器推断

但实际上 MCP 代码从不跑在主线程：

| 代码 | 实际运行位置 | 编译器认为 |
|------|-------------|-----------|
| `actor MCPManager` 的方法 | actor 串行执行器（后台线程） | `@MainActor` |
| `MCPServer.handleData` | `FileHandle.readabilityHandler`（DispatchQueue） | `@MainActor` |
| `Process.terminationHandler` 回调 | 系统进程管理线程 | `@MainActor` |

编译器推断「你在主线程」，实际运行在线程 A/B/C——于是每次从这些上下文访问「MainActor 标记」的属性或方法，编译器就报警。

### 为什么 `MCPLog` 的桥接没起作用

`MCPLog` 已经用 `Task { @MainActor in ... }` 做了线程跳转，方法是纯函数式的桥接。但 Swift 6 的推断是**静态签名级别**的，不看 `Task { }` 的语义只看 API 签名——看到 `LogBuffer` 就一路往上追溯，不在乎你包了几层 Task。

---

## 修复策略

### 核心思路

**声明式地告诉编译器「我知道我在干什么，别瞎猜」**——给所有被错误推断的代码显式标记 `nonisolated`，而不是重构架构去迁就编译器。

### 分层修复

| 层 | 手段 | 适用场景 |
|----|------|---------|
| `nonisolated` 方法 | 告诉编译器「这方法不绑任何 actor」 | 被误推断为 MainActor 的 static 方法、计算属性、实例方法 |
| `nonisolated(unsafe)` 属性 | 「这属性我自己管线程安全（NSLock），别查」 | class 的 mutable 存储属性、struct 中因 `Any` 无法 Sendable 的存储属性 |
| `@unchecked Sendable` | 「这类型的 Sendable 合规性别查」 | 包含 `[String: Any]` 或 `NSLock` 等非 Sendable 成员的类型 |
| `nonisolated init() {}` | 显式声明 init 不绑 actor | class 默认合成的 init 被推断为 MainActor 时 |
| `nonisolated static func ==` | 手写 Equatable | enum 自动合成的 Equatable 被推断为 MainActor 时 |
| `@preconcurrency import Foundation` | 全局降低 Foundation 的 Sendable 检查级别 | 作为兜底（效果有限，对成员级推断无效） |
| `lock.withLock {}` | 替代 `lock.lock()/unlock()` | Swift 6 禁止在 async 上下文用 NSLock 裸调 |

### 修复清单

| 文件 | 修复项 | 数量 |
|------|--------|------|
| `JSONRPC.swift` | `MCPLog.info/error` → `nonisolated`；`MCPError.errorDescription` → `nonisolated`；`JSONRPC.requestData/notificationData` → `nonisolated`；`JSONRPCResponse.isError/parse` → `nonisolated`；`JSONRPCResponse.result` → `nonisolated(unsafe)`；`MCPToolDef.parseList` → `nonisolated`；`MCPToolDef.inputSchema` → `nonisolated(unsafe)`；两个 struct 加 `@unchecked Sendable` | ~12 处 |
| `MCPManager.swift` | `MCPActivityTracker` 的 `used` → `nonisolated(unsafe)`；`markUsed/clear/snapshot` → `nonisolated`；`init()` → `nonisolated`；`shared/readMoreToolName/fallbackResultBudgetBytes` → `nonisolated` | ~8 处 |
| `MCPConfig.swift` | `MCPManagerConfig.default` → `nonisolated`；`import Foundation` → `@preconcurrency` | ~2 处 |
| `MCPServer.swift` | class 加 `@unchecked Sendable`；12 个 `var` 存储属性全部 `nonisolated(unsafe)`；所有 9 个方法加 `nonisolated`；`MCPServerState` → `Sendable` + 手写 `nonisolated static func ==`；8 处 `lock.lock()/unlock()` → `withLock {}` | ~30 处 |

总计约 **50 处改动**，全部是声明式注解，不改任何运行逻辑。

---

## 经验教训

1. **`ObservableObject` 是传染源**。如果某个类型继承了 `ObservableObject` 但不是纯粹为驱动 UI 存在（如日志收集器），它会把整个文件传染成 `@MainActor`。更好的设计是 UI 和逻辑分离——日志写入用纯函数、UI 订阅靠单独的薄 ViewModel。

2. **Swift 6 推断是静态签名级别的**。即使你已经在运行时做了线程安全处理（`Task { @MainActor }` / NSLock），编译器不认——只有 `nonisolated` 这种声明式标记才能让它安静。

3. **修复顺序很重要**。必须从调用链的最外层（`MCPLog`）逐层向内（struct 存储属性）打标记。如果只修外层不修内层，编译器会顺着新的引用关系继续找到下一个推断点。

4. **所有 `[String: Any]` 的属性都必须特别处理**。因为 `Any` 不是 `Sendable`，包含它的 struct 无法自动获得 `Sendable` 合规，Swift 6 会过度推断。唯一解是 `nonisolated(unsafe)` 直接标在属性上。

---

## 相关文档

- `docs/design/mac-apps/client-side-mcp.md` — MCP 模块架构设计
- `docs/design/mac-apps/client-tool-result-budget.md` — 客户端工具结果预算链治理
- [WWDC23 — Eliminate data races using Swift Concurrency](https://developer.apple.com/videos/play/wwdc2023/10170/)
