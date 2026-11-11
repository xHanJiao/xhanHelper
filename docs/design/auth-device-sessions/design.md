# 多设备登录：device 维度 token + 统一 401 登出 + 未发消息恢复

> 日期：2026-11-11
> 状态：**已实施（2026-11-11）**
> 关联：`../phase-2/p7-user-auth.md`（token 模型 / 认证中间件）、`../local-sqlite/design.md`（本地模式不走 user_tokens）

估算（开工）：4 面 / 3 缝（服务端 ↔ 三端契约、APIClient/SSEClient → DeviceManager 接线、Web 草稿恢复）/ 2 次真机往返 / 3 个待拍决定 · ~8–11h
估算（定稿）：4 面 / 3 缝 / 2 次真机往返 / 0 个待拍决定 · ~8–11h

## 一、问题与根因

**表现**：在另一台设备登录后，Web 或 macOS 客户端会掉线，但界面仍显示已登录；直到发消息失败才发现，且没有直接回登录页。

根因分三层：

1. **服务端按「最早创建」淘汰 token**
   `service/user_repo.go::Login` 每次登录 INSERT 新 token；同用户已有 `max_tokens_per_user`（`conf/config.yaml`，默认 4）条时，
   删除 `created_at ASC` 的最老一条。心跳续期只改 `expires_at`，不按设备去重，所以**最早登录、一直在用的那台（通常是 Mac）最先被淘汰**。
2. **淘汰是静默的，且无法区分原因**
   淘汰直接 `DELETE` 行，之后该 token 只得到 `401 invalid token`，与「过期 / 改密 / 清库」不可区分（`middleware/auth.go`、`view.JSONError`）。
3. **客户端发现与处理不一致**
   - Web：心跳 30 分钟（`stores/auth.js`），任何 401 → logout → `App.vue` watch 跳登录页；但**已清空的草稿/队列消息丢失**，登录页无提示。
   - macOS/iOS：定时续期 1 小时（`DeviceManager.refreshInterval`）；聊天 SSE 401 只弹 toast（`ChatViewModel`），
     列表/加载 401 只记日志或置 `isUnauthorized`——`isLoggedIn` 仍为 true，**可能一直不回登录页**。

本地 SQLite 模式使用固定本地 token、不查 `user_tokens`，不受影响。

## 二、目标 / 非目标

**目标**

- 同一台设备重新登录**只占一个 token 名额**，不再挤掉别的设备；总上限保留并提到 8。
- 任何业务请求返回 401 → 立刻切到登录页，并提示「登录已失效，请重新登录」。
- 被踢时未发出的消息自动保留，重新登录后可继续发送（不静默丢失）。

**非目标**

- 不做「被其他设备挤下线」的精确原因（不做 tombstone / revoked_reason）。
- 不做设备管理页（在线设备列表 / 手动踢出）。
- 不改本地模式（`APP_ENV=local`）的认证路径。

## 三、方案总览

| 层 | 改动 |
|---|---|
| 服务端 | `UserToken` 加 `device_id`；登录时先删同 user + 同 device 的旧 token；上限 4 → 8 |
| Web | 生成/持久化 `device_id` 随登录提交；401 标记「会话过期」并保存未发消息；登录页提示；回原会话恢复为暂停的排队消息 |
| macOS/iOS | 生成/持久化 `device_id` 随登录/注册提交；APIClient / SSEClient 的 401 统一发通知 → `DeviceManager` 清会话；未发消息存本地，登录后恢复 |

### device_id 契约

- **可选字段**，由客户端生成并持久化（UUID 字符串，≤64 字符）：
  - Web：`localStorage["xhanhelper_device_id"]`，`crypto.randomUUID()`（无则随机串兜底）。
  - macOS/iOS：`UserDefaults["xhan_device_id"]`。
- 服务端不校验格式，仅作为同一用户下的会话分组键；空串表示老客户端，行为与现在完全一致。
- `device_id` 不是安全凭证：随机 UUID 不可猜；即使伪造也只能踢掉自己账号自己的会话。

## 四、服务端改动

### 4.1 数据模型

`model/user.go::UserToken` 增加：

```go
DeviceID string `gorm:"size:64" json:"-"`
```

`app/init_db.go` 已对 `UserToken` 做 AutoMigrate，新列自动添加；旧行为空串，继续有效。无需数据迁移。

### 4.2 登录 / 注册逻辑

`service/user_repo.go`：

- `Create(ctx, username, password, deviceID)`、`Login(ctx, username, password, deviceID)`、`issueTokenTx(..., deviceID)`。
- `Login` 事务顺序调整为：
  1. `deviceID != ""` 时 `DELETE FROM user_tokens WHERE user_id = ? AND device_id = ?`（同设备旧会话作废）；
  2. 统计该用户 token 数，超过上限时按 `created_at ASC` 删最老；
  3. 插入新 token（带 `device_id`）。
- `deviceID == ""` 时跳过第 1 步，保持现有语义。
- 注册（`Create`）同样把 `device_id` 写进首个 token，后续同设备登录才能替换它。

`controller/auth.go`：register / login 请求体增加可选 `device_id`，透传 repo。
`controller/admin_user.go`：管理员建用户传空 `deviceID`。

### 4.3 配置

- `conf/config.yaml`：`max_tokens_per_user: 4` → `8`。
- 删除 `conf/config.dev.yaml`、`conf/config.test.yaml` 里各自的 `max_tokens_per_user: 4` 覆盖，统一走 base。

### 4.4 测试

`service/user_repo_test.go` 新增：

- 同设备连续登录两次：旧 token 失效，且该用户 token 总数不增加。
- 不同 device_id 登录：互不影响，总数累加。
- 超过上限（用 8 + 1）：仍淘汰最早创建的一条。
- `device_id` 为空：行为与现有测试一致（不删除任何 token）。

现有 `Create` / `Login` 调用点与测试补参数。

## 五、Web 前端

### 5.1 device_id

新增 `src/utils/deviceId.js`：读 localStorage，缺失时生成并写回；`auth.js` 的 login / register 请求体带 `device_id`。

### 5.2 统一 401 = 会话过期

- `stores/auth.js`：新增 `sessionExpired` 状态；`logout({ expired })` 区分手动退出与被动失效；登录成功清零。
- `api/client.js`：401 且本地有 token 时先置 `sessionExpired` 再 logout（登录接口此时 token 为空，不会误标）。
- `composables/useChatSSE.js`：3 处 401 / `AuthError` 分支在登出前保存未发消息。

### 5.3 未发消息恢复

新增 `src/utils/unsentDraft.js`（sessionStorage，单槽）：

```js
{ conversationId: string | null, items: [{ content: string, imageIds: string[] }] }
```

- 保存时机：聊天 401 时，把**当前发送项** + `chat.pendingSends` 剩余项一起写入，然后登出。
- 登录页 `LoginView.vue`：显示「登录已失效，请重新登录」；登录成功后按 draft 的 `conversationId` 跳回原会话（无 id 则 `/chat`）。
- `ChatView.vue`：路由 settle（历史加载 / reset 之后）消费 draft，恢复成 `pendingSends` + `queuePaused = true`，
  复用现有「已暂停 · 继续发送」UI；图片沿用已上传的 `imageIds`（Web 发送前已上传，可完整恢复）。

### 5.4 前台即时校验

`visibilitychange → visible` 时，若距上次校验超过 5 分钟，调一次 `/api/auth/refresh`。
401 → 走统一登出；网络错误忽略（等下次心跳）。避免「切回来还显示已登录，发消息才发现」。

## 六、macOS / iOS

### 6.1 device_id

`DeviceManager`：首次启动生成 UUID 存 `UserDefaults["xhan_device_id"]`；login / register 请求体带 `device_id`。

### 6.2 401 统一登出

- `APIClient`：所有 401 出口收敛到一个 `unauthorized()` 辅助函数——先 `NotificationCenter.post(.authUnauthorized, userInfo: ["token": token])`，再抛错。
- `SSEClient`：连接阶段 401 同样用该通知（带本次 token）。
- `DeviceManager`：订阅通知，**只在通知里的 token 等于当前 token 时** `clearAuthState()` + `sessionExpired = true`；
  避免旧 token 快照（如活动埋点传输）误伤刚登录的新会话。
- 登录 / 注册自身的 401 由 DeviceManager 直接处理，不经过 APIClient，不会被误当成会话过期。

### 6.3 未发消息恢复

新增 `Services/UnsentDraftStore.swift`（UserDefaults，单槽 Codable）：

```swift
{ conversationId: String?, title: String?, text: String, imageIds: [String] }
```

- 保存时机：`ChatViewModel` 的 SSE `onError` 收到 401 时，在登出前保存本轮 `text` + 已上传 `attachmentIds` + 会话 id/title。
  队列里剩余项只保留文本（罕见路径，未上传的本地图不保留，见 §八）。
- 恢复时机：`ContentView` 登录后消费：
  - macOS：`chatTabManager.openConversation(id:title:)` 回到原会话（无 id 则留在空 tab），填回 `draftText`；
  - iOS：按需 `loadConversation` 后填回 `draftText`；
  - `imageIds` 用 `APIClient.fetchImageData(id:)` 还原成 `DraftAttachment`。
- 恢复只填输入框，**不自动发送**。

### 6.4 前台即时校验

`ContentView.onReturnedToForeground()` 里加节流校验（5 分钟）：调一次 `deviceManager.verifySession()`（内部走 `/api/auth/refresh`），401 即登出。
macOS 的 `didBecomeActive` 与 iOS 的 `scenePhase == .active` 共用该入口。

### 6.5 登录页提示

iOS `LoginView` 与 `LoginMacView`：`deviceManager.sessionExpired == true` 时显示「登录已失效，请重新登录」；登录成功清零。

## 七、决定（已拍）

| # | 决定 |
|---|---|
| D1 | token 上限**保留**，4 → 8 |
| D2 | 采用 **device 维度 token**：同设备重登替换旧 token，不同设备互不影响 |
| D3 | 被踢时**立刻回登录页**，未发消息自动保留 |
| D4 | 不做「被其他设备挤下线」的精确原因提示，统一提示「登录已失效」 |
| D5 | Web 端恢复的消息用**暂停的排队 UI**呈现（不自动发送）；原生端恢复进输入框（不自动发送） |
| D6 | 会话失效原因不落库（不加 tombstone / revoked_reason） |

## 八、已知限制 / 风险

- **老 token 无 device_id**：升级后第一次登录会短暂多占一个名额（旧行仍在），靠后续淘汰自然消化；上限 8 足够缓冲。
- **原生排队项的本地图不保留**：401 时队列里尚未上传的图片只能保留文本；直发失败的那条消息图片已上传，可按 `imageIds` 完整恢复。
- **发现延迟**：新增的只是「回前台 5 分钟节流校验」。若用户长时间停在界面不动，仍要等到下一次操作/心跳才会发现；这是有意的流量取舍。
- **401 通知的 token 匹配**：必须严格比对当前 token，否则后台旧 token 快照（活动埋点等）可能误清刚登录的会话。
- **iOS 入口文件在 typecheck 盲区**：`.authUnauthorized` 通知的订阅与登录页提示改完需真机重编确认，不能只靠 `swiftc -typecheck`。

## 九、验收

- 服务端：`go build ./... && go test ./...`，含 §4.4 新增用例。
- Web：`npm test && npm run build`；手动走一遍「三设备登录 → 被踢 → 回登录页 → 消息还在排队区」。
- macOS / iOS：`swiftc -typecheck`（各自真实参数）+ 你在 Xcode 真机编译；真机验证：
  1. A 设备登录，B 设备用同账号登录（不同 device_id）→ A 不被踢；
  2. A 设备重登 → 仅 A 的旧 token 失效；
  3. 模拟被踢（清 token 行）→ 下一次操作立刻回登录页，提示正确，未发文本仍在。
- 文档：开发完成后同步 `phase-2/p7-user-auth.md` 的 token 模型与上限说明。

## 十、后续变更

- 2026-11-11 实施：
  - 三端 401 处理落地：Web `api/client.js` + `useChatSSE`；macOS/iOS 由 `APIClient`/`ChatViewModel` 发 `.authUnauthorized`，`DeviceManager` 比对当前 token 后清会话。
  - 未发消息恢复：Web 用 `sessionStorage`（`utils/unsentDraft.js`），原生用 `UserDefaults`（`UnsentDraftStore.swift`）；SSE 401 与前台/心跳等非 SSE 401 都会保存。
  - 实机验证（2026-11-11）：
    - 服务端：同设备重登只占 1 个名额；8 台共存；第 9 台淘汰最老。
    - Web（Playwright）：被踢 → 登录页提示 → 重登回原会话 → 暂停队列可继续发送。
    - macOS（真机）：被踢 → 登录页提示；草稿落入 `xhan_unsent_draft`，重登后回输入框。
    - iOS：本机无模拟器 runtime，未运行验证。
  - 构建脚本顺带修复：`client/xHelper/scripts/bundle-backend.sh` 的 `$ENV_NAME（` / `$LOCAL_CONF）` 改为带花括号，避免命令行构建时被当成变量名一部分。
