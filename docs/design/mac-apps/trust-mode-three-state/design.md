# 信任模式三态化（安全 / 信任 / 完全信任）

> **状态：已移除（2026-10-16）** —— 三档与输入栏盾牌按钮均已删除，语义固化为「常规 L3 免确认 + 少数必弹项」，
> 见文末「后续变更」。以下为实现当时（2026 年 9 月）的方案，保留作历史记录。

## 背景

信任模式原为二态（关/开）：开启后跳过 L3 工具确认，但 **L4（shell 高危命令）与敏感上下文升级的 L3 仍强制确认**。现拆为三档，新增「完全信任」——连 L4 也不再确认。

信任模式是**纯客户端**概念，只影响客户端工具执行前的确认闸门（`awaitToolConfirmation`），服务端零感知。

| 档位 | `TrustLevel` | 跳过范围 | 仍确认 |
|---|---|---|---|
| 安全 | `off` | 无 | L3、L4、敏感上下文 L3 |
| 信任 | `trusted` | 常规 L3 | L4、敏感上下文 L3 |
| 完全信任 | `full` | 常规 L3 + L4 | **仅**敏感上下文升级的 L3 |

敏感上下文升级的 L3（`forcedL3`：高敏域名 / http 明文页 / 用户标签写入授权）是防注入诱导自动操作银行/邮箱的最后一道，**任何档位都不跳过**。

## 实现

- `ChatViewModel.swift`：新增 `enum TrustLevel { off, trusted, full }`（+ `next` 循环）；`trustedMode: Bool` → `trustLevel: TrustLevel`；`toggleTrustedMode()` → `cycleTrustMode()`（三档 toast）。
- `awaitToolConfirmation`：入参 `force` → `elevated`（= `forcedL3`），保留 `hardened`（= `shellHardened`）。判定：
  - `off` → 都确认
  - `trusted` → `!(elevated || hardened)` 时放行
  - `full` → `!elevated` 时放行
- `ChatViewModel+ClientTools.swift`：调用处改传 `elevated: forcedL3, hardened: shellHardened`。
- `TrustedPill`：三态 symbol/tint（off=shield/青绿，trusted=shield.checkered/橙，full=shield.slash.fill/红）+ 三档 tooltip。
- `ChatView`：`inputBarTint` 三分支（full 红）；进入「完全信任」前 `confirmationDialog` 二次确认（destructive），取消不切档；L4 确认条文案「信任模式不可跳过」。
- `InputBar`：三处 `#Preview` 改三态。

## 非目标

- 不持久化（会话级本地状态，换会话回到安全档，与现状一致）。
- 不改服务端。
- 旧 `trustedMode` 布尔状态不复用；切换即全新枚举。

## 验证

- `swiftc -typecheck` macOS + iOS 零 error。
- 真机：off 弹 L3；trusted 免常规 L3、L4 仍弹；full 免 L4，但高敏域名/明文页仍弹；trusted 档连点胶囊 → full 前弹二次确认，取消保持 trusted。

## 后续变更

### 2026-10-16 · 三档整体移除（按钮撤掉，固定按「信任档」语义跑）

用户决定：日常一直在「信任」档，这个开关没有实际价值 → **撤掉输入栏的盾牌按钮，确认规则改成固定一套**（不再有运行时开关）。唯一偏离原「信任档」的地方是 **fs 写的确认保留**（用户要求：子任务改自己的文件仍要弹卡带 diff）。

| 情况 | 行为 |
|---|---|
| L2（只读白名单、shell_peek、browser 读、chart / MCP） | 直接执行 |
| browser open（L3 一般风险） | 直接执行（＝原「信任档」） |
| fs 写、fs 读未授权路径（`isFS && level == 3`） | **必弹**（批准后该路径临时授权 1h） |
| forcedL3（高敏域 / http 明文页 / 用户标签授权） | **必弹** |
| L4：shell 写 / 执行，以及 browser 的 download / cgclick / drag | **必弹**，红色 |
| 硬性拦截（白名单外命令、高危路径、敏感文件名、未授权目录） | 直接拒绝，不弹 |

判据集中在 `client/xHelper/xHelper/Shared/Tools/ToolConfirmPolicy.swift`（纯逻辑，单测 `Tests/FsCoreTests/ToolConfirmPolicyTests.swift`）：`L4 → 红色必弹`、`fs 的 L3 → 必弹`、其余免确认。**`L3` 不等于「要确认」**，新工具要确认就给 L4。

fs 的判据取 `isFS && level == 3` 而不是「只认写」：`level == 3` 同时是「该路径未授权 → 批准后临时授权」的唯一入口（`authorize(...)` 全仓仅此一处调用），照原样保留才不会出现「既不弹确认、又写不进去」。

browser 的 `download / cgclick / drag` 是 review 后从 L3 提到 L4 的（写盘 / 注入系统事件，与「fs 写必弹」取向对齐）：既不留下「同样写盘、一个弹一个不弹」的不闭环，也顺带救活了跨域下载的来源标注（`confirmArgs["跨域下载"]` 只在进确认条时展示）。

影响文件：

- `client/xHelper/xHelper/Shared/ViewModels/ChatViewModel.swift`：删 `TrustLevel` / `trustLevel` / `cycleTrustMode()`；`awaitToolConfirmation` 删跳过分支与 `elevated` 参数（函数内不再有「跳过」）；`loadConversation(preserveTrustedMode:)` → `preserveTransientState:`
- `client/xHelper/xHelper/Shared/ViewModels/ChatViewModel+ClientTools.swift`：闸门 `if level >= 3` → `if need != .none`（`need` 由 `ToolConfirmPolicy` 算，`forcedL3` 只升不降）
- `client/xHelper/xHelper/Shared/Tools/ToolConfirmPolicy.swift`（新增：等级 → 是否确认的唯一映射）+ `Tests/FsCoreTests/ToolConfirmPolicyTests.swift`（新增单测）
- `client/xHelper/xHelper/Shared/Tools/LocalToolRegistry.swift`：等级表注释重写；browser 的 `download / cgclick / drag` 由 3 → **4**
- `client/xHelper/xHelper/Shared/Views/InputBarButtons.swift`（删 `TrustedPill`）、`Views/InputBarTypes.swift`（删 `case trusted`；UserDefaults 里残留的 `"trusted"` 由 `compactMap(InputBarButtonID.init(rawValue:))` 静默忽略，无需迁移）、`Views/ChatView.swift`（删三档 UI 分支 / 二次确认弹窗 / `inputBarTint` 收成两分支）、`Views/InputBar.swift`（三个 Preview）
- 文案：`Views/ConfigPanelView.swift`、`server/internal/agent/subagent.go`（file_explore 的 shell 说明）
- 同步文档：`../shell-tool/{design,command-expansion,readonly-free-and-peek}.md`、`fs-write-tools.md`、`fs-write-subagent.md`、`fs-grep-pagination-security.md`、`browser-agent-workspace-test-guide.md`、`fs-batch1-test-guide.md`、`../multimodal-images.md`、`../return-to-cloud/design.md`

### 2026-10-29 · 日历写操作进 L4

`calendar__create_event` / `update_event` / `delete_event` 登记为 **L4（红色必弹）**：会写进用户日历并同步到手机，删改也难以回滚。
`calendar__list_events` / `search_events` 属只读，走默认 L2 免确认。见 `../calendar-native/design.md`。

