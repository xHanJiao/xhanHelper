# 010: JS 弹窗期间主线程被 runModal 卡死 → 外部 SIGTERM 终止

**日期:** 2026-08-04
**状态:** 已修复（sheet 化 + 退出流程非阻塞）

## 现象

浏览器打开某些网站（如 Bing）弹出 JS `alert()`/`confirm()` 时，app 在无任何用户操作的情况下"崩溃"。

Xcode 调试器显示：

```
Thread 1 Queue: com.apple.main-thread (serial)
#0  mach_msg2_trap          （主线程空闲在普通 app 事件循环）
#1  mach_msg2_internal
...
#13 -[NSApplication nextEventMatchingMask:]
#14 -[NSApplication run]
...
Thread 1: signal SIGTERM
```

关键特征：**主线程栈是普通 run loop（`-[NSApplication run]`），不是模态；停止原因是 `signal SIGTERM` 而非 SIGABRT/SIGSEGV。**

## 排查过程（结论先行）

1. **`signal SIGTERM` = 进程被外部请求终止**，不是代码崩溃（SIGABRT 才是崩溃）。
2. grep 全客户端确认：app 代码**没有任何自我 SIGTERM/abort/fatalError 路径**（唯一的 `kill(pid, SIGKILL)`/`terminate()` 是对 MCP 子进程的清理）。
3. 仓库脚本 / Makefile / launchd 均无杀 xhanHelper 的逻辑。
4. 复现时用户无任何操作、与开发冒烟测试的 `pkill` 无关。
5. 已排查线程（主线程 / JavaScriptCore / libpas scavenger / WebCore:Scrolling）**全是空闲等待**，故障线程不在已捕获栈中。

## 根因

**JS 弹窗用 `NSAlert.runModal()`，把主线程卡进嵌套模态 run loop。** 当系统/环境在此窗口期发起终止请求（注销 / 自动注销 / 用户切换 / 关机 / 外部 kill）时：

```
applicationShouldTerminate → .terminateLater
  → Task.detached { await MCPManager.shutdown(...); await MainActor.run { NSApp.reply(true) } }
```

但**主线程卡在 runModal，`MainActor.run` 永远执行不到** → app 无法优雅退出 → 最终被外部 SIGTERM 兜底杀掉。

弹窗不是 SIGTERM 的**因**，而是让 app **躲不过**系统终止：主线程一卡，任何优雅退出路径都失效。

## 修复

1. **JS 对话框 sheet 化**（`BrowserToolProvider.swift`）：`onJSAlert`/`onJSConfirm` 改用 `beginSheetModal`（非阻塞），alert/confirm 完成由用户点按后回调。JS `alert()` 恢复"等用户确认"的真实语义，且主线程永不被卡。
2. **退出确认 sheet 化**（`App.swift`）：`windowShouldClose` 与 `applicationShouldTerminate` 的 `runModal` 改为 `beginSheetModal` + `NSApp.reply(toApplicationShouldTerminate:)`（`.terminateLater` 异步完成），加 `isQuitConfirmShowing` 防重入。
3. **清除浏览数据 sheet 化**（`RightSidebarView.swift`）：`confirmClearBrowsingData` 的 `runModal` 改 `beginSheetModal`。
4. 无窗口兜底仍回退 `runModal`（罕见，仅打日志）。

## 验证

- 测试页 `scripts/js-dialog-test.html`（本地 `python3 -m http.server 8899` 服务）全 6 组跑完无崩溃，含旧代码必崩的"连发 3 个 alert"。
- 真实站点（Bing）长时间使用无法复现。
- JS 弹窗期间 Cmd+Q / 关窗应干净退出（exit code 0），不再落 SIGTERM。

## 后续捕获方法（若再遇 SIGTERM）

1. 崩溃报告的 **`Termination Reason` 行**（`Namespace SIGNAL, Code 15 Terminated: <来源>`，写 `logout`/`shutdown`/`launchd`/`signal by <pid>`）。
2. 精确时间 + 当时是否发生注销/用户切换/合盖休眠。
3. `~/Library/Logs/DiagnosticReports` 是否有 `.ips`。

## 关联改动

- `docs/design/mac-apps/browser-tier1-ux.md`（Tier 1 UX，含 JS 对话框 sheet 化设计）
- `scripts/js-dialog-test.html`（JS 弹窗回归测试页）
