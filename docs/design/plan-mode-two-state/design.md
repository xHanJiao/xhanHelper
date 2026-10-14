# 计划模式二态化（撤掉严格档）

> 日期：2026-10-14
> 范围：macOS / iOS 共享客户端 + Go 服务端。撤回 `docs/design/plan-mode-strict/design.md` 引入的严格档。

## 背景与决定

2026-09 把计划模式做成三档（关 / 普通 / 严格），其中严格档（`mode = "strict"`）不下发任何工具、纯讨论。实测用不上：严格档与普通档的差别是「能不能用只读工具查数据」，而讨论方案时恰恰需要查；多一档只是让人多点一次、还多一个红色锁图标。**决定撤掉严格档，输入框回到二态按钮（关 / 计划）。**

| 档位 | mode | 工具能力 |
|---|---|---|
| 关闭 | `work` | 全量工具（渐进式暴露） |
| 计划 | `plan` | 仅只读工具（检索/查询），写操作执行级拦截 |

## 关键约束：旧数据必须归一到 plan

库里已存在 `mode = 'strict'` 的行。删掉 `ConvModeStrict` 后，`ModeRestrictsTools("strict")` 会变成 `false` —— 也就是说**不归一的话，旧严格会话会被当成不受限模式、静默放开全部工具**（比原来更危险）。所以：

- 新增 `model.NormalizeConvMode(mode)`：`""` → `work`、历史 `"strict"` → `plan`、其余原样。
- **不做数据迁移**，读侧归一即可（库里旧值留着无害）：
  - `controller/chat.go`：`convMode := model.NormalizeConvMode(conv.Mode)`（同时兜住空值），在 `ModeRestrictsTools` 判定与 `RunRequest` 之前执行；
  - `controller/conversations.go`：`convModeOrDefault` 转发 `NormalizeConvMode`，列表/详情响应的 `mode` 自动归一，客户端看到的就是 `plan`。
- `init_mode` 同样先归一，旧客户端发的 `"strict"` 落成 `plan`（不是落成 `work`）。

## 改动清单

服务端（删除 strict 支持）：

- `model/conversation.go`：删 `ConvModeStrict`；`ModeRestrictsTools` 只判 `plan`；新增 `NormalizeConvMode`（保留具名常量 `legacyConvModeStrict` 供读侧识别历史值）。
- `tool/registry.go`：删 strict 全拦分支（plan 只读分支保留）。
- `agent/orchestrator.go`：`mkLlmChatReq` 删 strict 空 tools 分支。
- `agent/promptbuilder/`：删 `strictModeNotice`（`static.go`）与 builder 的 strict 分支。
- `controller/chat.go`：删 `strictModeNotice` 注入与 `strictNoticeMarker`；`containsPlanNotice` 只匹配 plan 标记；模式判定改走 `NormalizeConvMode`。
- `controller/conversations.go`：`PATCH /mode` 只接受 `work`/`plan`（传 `strict` 得 400，客户端 toast 报错并回滚，不静默失配）。
- 测试：`registry_plan_mode_test.go`、`orchestrator_final_round_test.go`、`orchestrator_test.go`、`promptbuilder/builder_test.go` 去掉 strict 用例；新增 `model/conversation_test.go::TestNormalizeConvMode`（`""`→work、`"strict"`→plan、未知值透传）；`conversations_list_mode_test.go` 加一条历史 `strict` 行，断言列表响应归一为 `plan`（读侧唯一出口 `convModeOrDefault` 的端到端兜底）。

客户端（`Shared/` 编进 macOS + iOS，**两端同步变二态**）：

- `ChatViewModel.swift`：`ChatMode` 删 `.strict`，`next` 改为 off↔normal；`cycleMode()` 两条 toast。
- `InputBarButtons.swift`：`ModePill` 恒为 `checklist` 图标，色只留 accent/orange（不再有锁图标/红）。
- `ChatView.swift`：输入栏描边色删 `.strict` 分支（计划=橙）。
- `InputBar.swift` / `xHelperMac/ShortcutGuideView.swift`：Tab 说明改「关/计划」。
- `APIClient.swift` / `Conversation.swift`：注释同步。

## 行为变化（用户可感知）

- 输入框计划按钮只剩两态；Tab 循环序为 关↔计划。
- 旧的严格会话打开后显示为「计划」（橙），工具为只读 —— 这是有意的归属选择（最接近原「纯讨论」意图，且用户可自助切回关闭）。
- `PATCH /mode` 不再接受 `strict`。撤档前的旧客户端（循环序 off→normal→strict→off）切「严格」会收到 400 并回滚到 normal，再点又算出 strict、再次 400 —— **旧二进制回不到关闭档**，不是单纯「被拒」。
  本项目客户端只有一台、与本仓库同机发布，约定**先更新客户端、再起新服务端**，不为此留兼容分支（若将来真有多端旧客户端，正解是 PATCH 把 `strict` 落库成 `plan`）。

## 不在范围

- 不做数据迁移脚本（读侧归一已覆盖）。
- Vue 前端本来就没有计划模式切换入口，不改。
- 普通计划模式的只读语义、`planModeNotice` 注入与退出提示逻辑不变。

## 验证

- `cd server && go build ./... && go test ./...` 全绿（含历史 strict 行归一为 plan 的列表断言）。
- Swift：`Shared/**` + 两入口 `swiftc -typecheck`（macOS 26.3 / iOS 26.0）零 error。
- 真机（Xcode）：胶囊与 Tab 只在两档循环；打开一条历史严格会话，显示为「计划」且写操作被拦（不是工具全开）。
