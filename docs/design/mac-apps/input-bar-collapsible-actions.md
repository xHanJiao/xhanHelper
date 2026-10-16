# 输入栏「更多操作」折叠 + 向上展开一行（macOS）

## 目标

macOS 聊天输入框按钮过密。除**计划模式**与**智能体**常驻外，其余按钮（信任、模型、接力、附件）收进 chevron 后，默认不占位；点 chevron 在**按钮行上方**多出一行图标（同一胶囊内），12s 无操作自动收起。

**同时修掉旧实现的「出框」**：旧 `CollapsibleModeControls`（`3f60ee4` 移除）用 `.overlay + .offset(x: -76)` 把胶囊浮在 chevron 左侧，脱离布局且硬编码左偏——输入栏一窄，浮层就戳出胶囊左缘。

## 已确认的设计决策（勿改）

1. **折叠范围**：信任 / 模型 / 接力 / 附件。计划模式 + 智能体 + 发送常驻。默认态按钮行 = `[上下文圆环] … [计划][智能体][chevron][发送]`。
   （**2026-10-16 起折叠行只剩 无痕 / 附件** —— 模型 / 接力 / 精炼回答已下架，见文末「后续变更」。）
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

## 后续变更

### 2026-10-16 · 模型 / 精炼回答 / 接力三颗按钮下架（用户拍板）

- 三颗 pill（`ModelPill` / `BriefPill` / `HandoffPill`）的 `visibility` 由 `.always` 改为 `.hidden`，**并且把这三个 id 从 `InputBarButtonID.allCases` 移除**（`Shared/Views/InputBarButtons.swift` + `Shared/Views/InputBarTypes.swift`）。
  - 只改 `visibility` 不够：`.hidden` 只是渲染成空视图，而 `ChatView` 的两个 `ForEach` 按 `allCases` 枚举 —— 老用户若曾把「模型 / 接力」设为**常驻**，常驻行会永久留一个空占位，且它没有命中区（右键菜单挂在空视图上），连「收起」都点不到。下架的 id 必须同时从 `allCases` 移除。
  - `InputBarLayoutState.init` 顺手把持久化配置里残留的下架 id 过滤掉（`intersection(allCases)`）：否则「收起」集合里只剩这些 id 时，chevron 会展开出一条空行。
  - **功能代码一行未删**：恢复 = 把 id 加回 `allCases` + `visibility` 改回 `.always`。
- 顺带拆掉两个「关不掉」的持久化地雷（`Shared/ViewModels/ChatViewModel.swift` 载入会话处）：`deepMode` 与 `briefAnswer` 不再从会话键恢复（恒为 false，建会话时那两行 `UserDefaults.set` 也一并删掉）。否则旧会话会永久停在「深度模型」或「精炼回答 ≤500 字」，而入口已经没有了。
- 真正被拿掉的只有 **pro 档模型**与 **≤500 字约束**。「思考」本来就不由客户端说了算：`thinkingMode` 只是 `llm_thinking`（服务端 llm 配置的本地镜像，`refreshAndCacheLLMConfig` 写入）的态，且请求体里的 `thinking` 服务端**根本不读** —— `controller/chat.go` 构造 `RunRequest` 时只取 `DeepMode` / `BriefAnswer` / `VerifyEnabled` / `HiddenRound`。
- 折叠行现存按钮：**无痕 / 附件**；常驻行仍是 **计划 / 智能体 / chevron / 发送**。
- 未受影响：`InputBar.swift` 零改动（它头部「增删按钮不改本文件」的约定继续成立）；输入栏双态、12s 自动收起、常驻/收起的右键菜单照旧（唯一改动是 `#Preview` 里那颗 `ModelPill` 换成了 `EphemeralPill`，否则预览会少一格）。
