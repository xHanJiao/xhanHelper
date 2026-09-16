# 输入栏「更多操作」折叠 + 向上展开一行（macOS）

## 目标

macOS 聊天输入框按钮过密。除**计划模式**与**智能体**常驻外，其余按钮（信任、模型、接力、附件）收进 chevron 后，默认不占位；点 chevron 在**按钮行上方**多出一行图标（同一胶囊内），12s 无操作自动收起。

**同时修掉旧实现的「出框」**：旧 `CollapsibleModeControls`（`3f60ee4` 移除）用 `.overlay + .offset(x: -76)` 把胶囊浮在 chevron 左侧，脱离布局且硬编码左偏——输入栏一窄，浮层就戳出胶囊左缘。

## 已确认的设计决策（勿改）

1. **折叠范围**：信任 / 模型 / 接力 / 附件。计划模式 + 智能体 + 发送常驻。默认态按钮行 = `[上下文圆环] … [计划][智能体][chevron][发送]`。
2. **展开形态**：展开行插在按钮行**上方**、胶囊**内部**，右对齐。参与布局 → 宽度天然受胶囊约束，**任何窗宽都不会出框**。
3. **自动收起**：展开后 12s 无操作收起；点展开行内任一胶囊重置计时（沿用旧逻辑）。
4. **不用 overlay / offset**：这是旧 bug 的根因，明确禁止回退。

## 宽度预算（为何必须「另起一行」而非并排）

- 中间栏最小宽 `Layout.middleMinWidth = 340`；胶囊内宽 ≈ `340 - 2×chatHorizontalPadding(24) - 2×14` ≈ **236pt**。
- 单个 `IconButton` = 34pt，间距 4pt。
- 默认（收起）行：计划+智能体+chevron+发送+圆环 ≈ 4×34 + 3×4 + 16 ≈ 164pt → 放得下。
- 若展开的 4 个图标**并排进同一行**：再加 4×34+4×4 ≈ 152pt → **≈ 316pt > 236pt，必然挤压/溢出**。
- 展开行单独占一行：4 图标 ≈ `4×34+3×4 = 148pt < 236pt` → 恒放得下。

## 改动清单

| # | 文件 | 改动 |
|---|------|------|
| 1 | `Shared/Views/InputBarButtons.swift` | 新增 `ExpandPill`（chevron.up/down，`.secondary`） |
| 2 | `Shared/Views/InputBar.swift` | 新增第 6 个可选槽 `statusRowExtras`，在 `twoRowLayout` 里插到按钮行上方（`attachmentTray` 与按钮 HStack 之间）；便捷 init 的 `where` 补 `StatusRowExtras == EmptyView`（3 个 Preview 无需改） |
| 3 | `Shared/Views/ChatView.swift` | 新增 `inputBarExtrasExpanded` + `extrasCollapseTask`；`inputBarStatusRow` 收缩为 计划/智能体/chevron；新增 `inputBarExtrasRow`（信任/模型/接力/附件）；附件按钮从 `inputBarTrailingActions` 移入展开行；调用处传 `statusRowExtras` 并补展开动画 |

## 结构

```
胶囊（RoundedRectangle）
└─ VStack
   ├─ editor
   ├─ attachmentTray（待发缩略图）
   ├─ statusRowExtras   ← 展开行：Spacer + 信任/模型/接力/附件（右对齐，可空）
   └─ HStack: bottomLeading(圆环) · Spacer · statusRow(计划/智能体/chevron) · trailingActions(发送)
```

## 验收

- 默认只见 计划 / 智能体 / chevron / 发送（有附件时另有缩略图托盘）。
- 点 chevron → 按钮行上方长出一行 信任/模型/接力/附件；胶囊向上长高，消息区不被顶跳。
- **把中间栏拖到最窄（340）**再展开，四个图标仍完整落在胶囊内，不越界。
- 12s 无操作自动收起；展开行内点任一开关后重新计时。
- 计划模式 / 深色模式 / 流式中的置灰与 tooltip 与改造前一致。

## 验证方式

客户端按 `CLAUDE.md` 约定：agent 用 `swiftc -typecheck`（真实 settings）自检，不跑 `xcodebuild`；真机编译与视觉验收由用户在 Xcode 完成。
