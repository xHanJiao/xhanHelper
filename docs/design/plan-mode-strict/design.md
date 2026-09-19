# 计划模式三态化（关闭 / 普通 / 严格）

## 背景

计划模式原为二态：`work`（工具全开）与 `plan`（2026-09-15 只读化后：仅只读工具）。现要求拆为三档：

| 档位 | mode | 工具能力 | 历史对应 |
|---|---|---|---|
| 关闭 | `work` | 全量工具 | 现有 work |
| 普通 | `plan` | 仅只读工具（检索/查询） | 现有 plan |
| 严格 | `strict`（新增） | 无任何工具（不下发定义 + 执行级全拦） | 更早的「纯讨论」plan |

三档均保留每轮自动注入的检索/画像/主题上下文（只禁工具，不去上下文）。模式仍由客户端独占切换，服务端权威。

## 后端

- `model/conversation.go`：新增 `ConvModeStrict`；新增谓词 `ModeRestrictsTools(mode)`（plan 或 strict）；修正过时注释。
- `tool/registry.go`：`ExecuteByName` 在 plan 只读判断前加 strict 全拦分支（防止历史 tool_calls / 客户端绕过）。`tool/tool.go` 注释同步。
- `agent/orchestrator.go`：
  - `mkLlmChatReq`：strict → `tools = nil`（连 `expose_tools` 都不下发）。
  - 客户端工具目录生成、渐进暴露加载/持久化、客户端本地工具拦截四处改用 `ModeRestrictsTools`，让 strict 同样短路。
- `agent/promptbuilder/`：`builder.go` 加 strict 分支（工具定义/目录两段都换成 `strictModeNotice`）；`static.go` 新增 `strictModeNotice`。
- `controller/conversations.go`：`PATCH /mode` 校验放行 strict。
- `controller/chat.go`：`init_mode` 接受 strict；strict 每轮注入 `strictModeNotice`；退出提示检测 `containsPlanNotice` 同时匹配 plan 与 strict 的标记（从严格档退回关闭也注入一次退出提示）。

## macOS 客户端

- 新增 `enum ChatMode: String { off = "work", normal = "plan", strict }`，`ChatViewModel.chatMode` 由服务端 `mode` 字符串派生（未知值归一 `off`）；`isPlanLike` = 非 off。
- `toggleMode()` → `cycleMode()`：关 → 普通 → 严格 → 关；沿用「已有会话 PATCH 落库、失败回滚；空 tab 本地暂存走 init_mode」。
- `ModePill`：`off` = checklist/appAccent，`normal` = checklist/orange，`strict` = lock.fill/red；tooltip 说明当前档位与下一档。
- `ChatView`：输入栏描边色 strict 红、normal 橙；接力按钮对 `isPlanLike` 置灰。
- Tab 快捷键与闭环路径不变（同一闭包，自动变循环）。

## 不在范围

- Vue 前端无计划模式切换入口，不改。已知限制：macOS 置为 strict 的会话，Web 端无 UI 可切回（与现状 plan 相同）。
- 旧客户端只认 work/plan，读到 strict 会经 `?? .off` 误显示为关闭（服务端仍严格）——由客户端升级解决，服务端保持原值。

## 验证

- Go：`registry_plan_mode_test.go`（strict 全拦含只读）、`builder_test.go`（strict 无函数定义 + 有提示）、`orchestrator_final_round_test.go`（strict tools 为空）、`TestModeRestrictsTools`。
- `go build ./... && go test ./...` 全绿。
- Swift：`Shared/` + 两入口 `swiftc -typecheck`（macOS / iOS）零 error。
- 真机（Xcode）：三档各发一条，确认严格档 LLM 无工具可用、普通档只读可用、关闭档全量；检查退出提示注入。
