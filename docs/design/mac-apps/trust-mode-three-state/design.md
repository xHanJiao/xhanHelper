# 信任模式三态化（安全 / 信任 / 完全信任）

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
