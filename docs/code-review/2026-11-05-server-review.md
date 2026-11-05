# 代码审查报告 · server 端

**审查日期**: 2026-11-05
**审查范围**: `server/`（Go 后端全量；front-end / Swift client 下一轮）
**整体评估**: ⚠️ 底子好，但有一处跨用户越权 + 两处日志泄密需要修

**分支**: `fix/server-security-a`（本轮只做批次 A）

---

## 零、基线

| 项 | 结果 |
|---|---|
| 规模 | 504 个 `.go`，66.8k 行生产代码；173 个测试文件 46.3k 行 |
| `go build ./...` | ✅ |
| `go vet ./...` | ✅ |
| `gofmt -l` | ✅ |
| `go test ./...` | ✅ 全绿 |
| `go test -race ./internal/{agent,activity,async,tool,service}/...` | ❌ 36 处 DATA RACE，5 个测试失败（均在测试替身，见 P2-6） |
| `staticcheck` | ⚠️ 工具自身 panic（v0.4.7 不支持 Go 1.26）；仓库无 CI、无 lint 配置 |

---

## 一、P0 — 必须修

### 1.1 `/api/chat` 缺少会话归属校验（跨用户读写）· `internal/controller/chat.go:145`

```go
conv, err = convRepo.FindByID(c.Request.Context(), *req.ConversationID)
```

`ConversationRepo.FindByID` 只按 id 查（`service/conversation_repo.go:65`），不带 `user_id`。项目里**其它所有**会话端点都走 `requireConversationOwner`（`controller/conversations.go:199`：getMessages / getMessagesCursor / usage / stopChat / delete / restore / mode / plans / ephemeral 全部校验），只有 `/api/chat` 漏了。

影响（多用户或开放注册时）：

- 拿到他人 `conversation_id` 即可往对方会话写入消息；
- 历史按 `conv.ID` 读（`chat.go:255/270`），回复基于对方历史 → **读到别人对话内容**；
- 可顶掉对方正在运行的对话（`ChatStopRegistry` 按 convID 键控）。

**修复**: 改用 `requireConversationOwner(c, convRepo, *req.ConversationID)`，非 owner 403 / 不存在 404；补 handler 级回归测试（sqlite + 直接调用 handler，deny 路径在 `orch` 之前返回，无需构造 orchestrator）。

---

## 二、P1 — 本轮修

### 2.1 请求体全量入日志，含明文密码 · `internal/middleware/logging.go:10`、`appinit/router.go:196`

`RequestLogger` 无条件挂载，`peekBody` 对 `application/json` 截前 2000 字节写日志。`/api/auth/login`、`/api/auth/register`、`/api/auth/change-password` 的 **password 明文** 会进 `logs/`。

**决策（本轮）**: 不做全局关闭（日志内容是日常排障依赖），只对**凭据类路径**整段打码 `<redacted:auth>`，且不读取 body（handler 收到完整请求体）。其余请求维持原行为。聊天正文入日志属隐私取舍，另议。

### 2.2 启动日志打印完整 MySQL DSN（含密码）· `internal/app/init_db.go:66`

```go
log.Printf("[config] db.driver=%s db.dsn=%s", cfg.DB.Driver, cfg.DB.DSN())
```

`DSN()` 为 `user:password@tcp(...)`。**修复**: 改为打印 `driver / host:port / database / user`（sqlite 打路径），不拼密码。

---

## 三、P2 — 后续批次

### 3.1 `/api/chat` 吞掉默认会话查询错误 · `chat.go:156`

`conv, _ = convRepo.FindDefaultByUser(...)`：DB 出错时 `conv==nil` 会**新建默认会话**，用户表现为"历史消失"，错误静默丢失。应记日志 + 500。

### 3.2 反射子系统工具 goroutine 无 panic 兜底 · `agent/react_loop.go:223`

主循环（`orchestrator.go:1890`）每个工具 goroutine 都 `recover`，`ReactLoop.executeConcurrently` 没有。工具 panic 会终止整个进程（`gin.Recovery` 只覆盖 HTTP handler）。改用 `util.SafeGo` 或补同样的 recover + 日志。

### 3.3 测试套件在 `-race` 下不干净（36 处）

- `agent/client_tool_subagent_test.go:25/30`：`captureSink` 被 `MiniReAct` goroutine 写、测试 goroutine 读；
- `agent/subagent_test.go:826`：`costRecorder.Record` 在 `llmcost.ChatSync` 后台 goroutine 里写，测试在 851 读。

后果：`-race` 不可用，未来真并发问题会被噪声淹没。修测试替身（mutex / channel），不动生产代码。

### 3.4 评测后台 `project` 参数路径穿越 · `controller/eval_admin_handler.go:31`

`filepath.Join(evalDir, project, "eval.db")` 直取 URL 参数；`project=..%2F..` 可指向任意目录，`eval.OpenRepo` 还会建表写入。仅 admin 可调。加 `^[A-Za-z0-9_-]+$` 校验即可。

### 3.5 静默吞错清单（项目规则：没有"允许忽略"，只有"已记录的有意降级"）

| 位置 | 问题 |
|---|---|
| `tool/reminder.go:235`、`260` | `json.Unmarshal(args, &req)` 返回值完全丢弃，坏参数当空参数执行 |
| `controller/subagent_admin.go:71` | `json.Unmarshal(row.Extra)` 忽略错误 |
| `tool/theme.go:137` | `ReactivateDraft` 的 error 被 `_ =` 丢弃 |
| `controller/auth.go:112` | `userRepo.Logout(...)` 的 error 未接 |
| `chat.go:459` | `convRepo.Rename` 失败无日志 |
| `orchestrator.go:547` | `convRepo.Touch` 失败无日志 |
| `agent/cron_runner.go:701/707/716/718/754/760/771/773` | `MarkDone/ScheduleRetry/MarkFailed` 失败无日志 → 任务可能重复执行 |
| `service/image_store.go:123` | `FindBySHA` 出错当"没查到"，绕过指纹去重 |
| `eval/repo.go` 十余处 | `_ = Scan(...)` / `_ = rows.Err()`（评测子系统，优先级低） |

### 3.6 登录/注册无频率限制 · `controller/auth.go`

bcrypt 逐次比对 + 无限流；公网暴露可被撞库/CPU 消耗。加按 IP+用户名 的失败计数（内存滑动窗口），或 nginx 层限流并写进部署文档。

### 3.7 账单导入循环内逐条 INSERT · `controller/bills.go:179`

违反项目"禁止循环内逐条访问数据库"。先校验收集合法行，末尾 `tx.Create(&valid)` 批量插入；逐行错误照常回报。

---

## 四、P3 — 清理项

- **死代码**：`tool/theme.go`、`tool/get_bill_stats.go`、`tool/get_topic_stats.go`、`tool/get_topic_detail.go`；`agent/theme_*.go`、`agent/state_*.go`。AGENTS 标注"保留未接线"，建议给明确结论（删 / 加保留注释），别长期挂账。
- **分享创建无事务**：`service/document_share_repo.go:34` 先删旧分享再建新的，建失败丢链接。
- **列表 `limit` 无上限**：`?limit=100000` 可拉全表（bills / notes / memories 均 `Atoi` 后直传 repo）。
- **重复实现**：`loadActiveEvents` 在 `agent/event_assembler.go:187` 与 `agent/retrieval/event_source.go:354` 一字不差两份。
- **prod `AutoMigrate` 失败仅记日志继续**（`app/init_db.go:82-88`），与本地 Fatal 策略不一致，schema 漂移后运行时才炸。
- **密码/令牌策略**：无最短长度；bcrypt cost 10；`UserToken.Token` 明文存库。
- **双 sqlite 驱动**：cgo `mattn` + 纯 Go `glebarez/modernc` 并存。
- **大文件**：`agent/orchestrator.go` 2445 行、`chat` handler 296 行，可读性可拆，不建议与安全修复混批。
- **测试覆盖率**：`controller` 13.9%、`llm` 21.6%，handler 层基本靠纯函数测试，鉴权/权限这类最容易漏测。

---

## 五、本轮修复范围（批次 A）

| # | 项 | 文件 | 测试 |
|---|---|---|---|
| A1 | `/api/chat` 会话归属校验 | `controller/chat.go` | 新增 handler 级 deny 回归测试 |
| A2 | 凭据类请求体日志打码 | `middleware/logging.go` | 新增 `peekBody` 打码单测 |
| A3 | DSN 日志脱敏 | `app/init_db.go` | 无（纯日志文案） |

**实施结果（2026-11-05）**

- A1：`chat.go` 的显式会话分支改走 `requireConversationOwner`；新增 `controller/chat_ownership_test.go`
  （他人会话 → 403 且原会话不被改动；不存在的会话 → 404）。
  复审补正：该公共函数原先把 `FindByID` 的 DB 故障折叠成 404 且不打日志，违反「没有允许忽略的错误」；
  已改为 `err != nil` → 记日志 + 500、`conv == nil` → 404（所有会话端点共同受益），并补
  `TestChatConversationLookupDBErrorIsNot404`。
- A2：`middleware/logging.go` 新增 `redactedBodyPaths`（login / register / change-password），
  命中时记 `<redacted:auth>` 且**不读取** body；新增 `TestRequestLoggerRedactsAuthBodies`。
- A3：`app/init_db.go` 改打 `driver / address / name / user`（sqlite 打路径），不再拼接密码。
- 验证：`go build ./...`、`go vet ./...`、`gofmt -l`、`go test ./...` 全部通过。

## 六、后续变更

- 2026-11-05：建立本报告；批次 A 开始实施（分支 `fix/server-security-a`）。
- 2026-11-05：批次 A 完成（A1/A2/A3），全量 build/vet/test 通过；B/C 批次待排期。
- 2026-11-05：复审补正——`requireConversationOwner` 的 DB 错误不再折叠为 404，改为记日志 + 500，并补回归测试。
