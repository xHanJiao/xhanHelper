# Server 代码审查报告

> 日期: 2026-05-04 | 范围: server/ 目录 47 个 Go 源文件

---

## P0 — Critical (3 项)

### 1. `agent/plan_executor.go` — 完全不可用 ~~✅ 已删除~~

**行号**: 119-136

**问题**: `executeNode` 接收 `node PlanNode` 按值传递，所有对 `node.Status`/`node.Result` 的修改在函数返回后丢失。同时 `wg.Wait()` 之后没有任何代码把已执行节点 ID 写入 `done map`。结果：`findReadyNodes` 每轮返回同样的 "pending" 节点 → 重复执行 → 依赖节点永远无法被调度 → 20 轮上限后假成功。

**修复**: 确认 `plan_executor.go` 和 `plan.go` 无任何外部引用，直接删除。

---

### 2. `tool/bookkeeping.go` + `controller/note.go` — 跨用户数据操作无所有权检查

**行号**: bookkeeping.go:222-274, note.go:32-37

**问题**:
- `BookkeepingTool.updateBill` 通过 ID 查找账单后，不验证 `existing.UserID == ec.UserID`，用户 A 可修改用户 B 的账单
- `BookkeepingTool.deleteBill` 同理
- `NoteHandler.DeleteNote` 通过 `c.Param("id")` 删除，不校验该笔记是否属于当前认证用户

**修复方向**: 所有变异操作（update/delete）加 `UserID` 交叉验证。

---

### 3. `controller/router.go` — CORS 配置无效

**行号**: 19-24

**问题**: `AllowCredentials: true` + `AllowOrigins: []string{"*"}` 同时设置。根据 Fetch 标准，浏览器会拒绝通配符 origin + credentials 的组合，跨域请求实际上不可用。

**修复方向**: `AllowOrigins` 改为具体的 origin 列表，或 `AllowCredentials` 设为 false。

---

## P1 — High (7 项)

### 4. `agent/extractor.go` — `Store` 对已有记录调 `Create` 而非 `Update`

**行号**: 98-104

**问题**:
```go
existing, _ := e.store.FindSimilar(ctx, conv.UserID, key)
if existing != nil && existing.MemoryValue == value {
    e.store.Store(ctx, existing) // 内部调 memRepo.Create → INSERT，不是 UPDATE
}
```
注释说"触发 updated_at 更新"，但实际执行 INSERT，要么报 unique key 错误，要么插入重复行。

**修复方向**: 抽取 `Update` 方法或改用 `memRepo` 的更新操作。

---

### 5. `llm/embedding.go` — HTTP Client 无超时

**行号**: 33

**问题**: 所有 LLM client 都设置了超时（120s/180s），唯独 Embedder 使用 `&http.Client{}`（无超时）。`saveMsgEmbedding` 和 `embedNote` 以 fire-and-forget goroutine 调用，若 API 卡住则导致不可恢复的 goroutine 泄漏。

**修复方向**: 加 `Timeout: 30 * time.Second` 或其他合理超时。

---

### 6. `llm/deepseek.go` — `Chat()` goroutine 无取消保护

**行号**: FlashClient.Chat ~149, ProClient.Chat ~241

**问题**: 两个 `Chat` 方法启动 goroutine 向 channel 逐字写入。若 consumer 停止读取（客户端断开），goroutine 永久阻塞在 `events <-`。Doubao 版本有 `select { case <-ctx.Done() }` 保护，DeepSeek 版本没有。

**修复方向**: 加 `select { case events <- ...; case <-ctx.Done(): return }`。

---

### 7. `tool/bookkeeping.go` — `getCategoryStats` 的 `_total` 计数错误

**行号**: 388

**问题**:
```go
result["_total"] = catStat{Amount: total, Count: len(bills)}
```
`len(bills)` 统计的是未过滤的总数。若指定了 `start_date/end_date`，`_total.count` 会大于实际分类计数之和。

**修复方向**: 计数应来自已过滤的账单数据而非原始列表。

---

### 8. `model/device.go` — GORM HasMany 指向不存在的列

**行号**: 13-14

**问题**:
```go
Conversations []Conversation `gorm:"foreignKey:DeviceID"`
Bills         []Bill         `gorm:"foreignKey:DeviceID"`
```
`Conversation` 和 `Bill` 用的是 `UserID`，没有 `DeviceID` 列。这些 GORM 关系永远不会工作。

**修复方向**: 删除 Device 模型中的这两个字段，或改为正确的关联方式。

---

### 9. `service/bill_repo.go` + `service/conversation_repo.go` — `Save()` 全量覆盖可能归零字段

**行号**: bill_repo.go:28, conversation_repo.go:77

**问题**: `Save()` 写入 struct 的所有字段包括零值。若 caller 传的 struct 中 `Status` 默认为 0，会把 `Status=2`（已作废）覆盖为 0（无效值）。

**修复方向**: 改用 `Select` 做部分更新，或用 `Updates(map[string]interface{})`。

---

### 10. `service/user_repo.go` — 用户注册/登录非事务性

**行号**: 22-39, 42-56

**问题**: `Create` 先插 User 再插 Token，`Login` 也是两步。无事务包裹。若 Token 插入失败，User 记录悬空（Create）或登录无反馈（Login）。

**修复方向**: 用 `db.Transaction` 包裹两步操作。

---

## P2 — Medium (10 项)

### 11. `agent/orchestrator.go` — God struct（6+ 职责）

**问题**: Orchestrator 直接掌管 ReAct 循环、向量检索过滤、消息持久化（3 个 repo）、标题生成、Warmup、并发工具执行、SSE 事件发射。难以单元测试（需 mock 7 个依赖）。

**修复方向**: 拆分为 Retriever / Persister / Executor 等独立服务。

---

### 12. `agent.New` 11 个参数 + `controller.NewRouter` 7 个参数

**行号**: orchestrator.go:59-62, router.go:13

**问题**: 位置参数极多，极易因参数顺序错误引入 bug。

**修复方向**: 用 functional options 或 config struct。

---

### 13. `agent/orchestrator.go` — Flash 路径 tool result 用 `role: "user"`

**行号**: 299-303

**问题**: Flash JSON 解析路径将工具结果包装为 `role: "user"` + `"[工具执行结果] "` 前缀，而 native tool-call 路径用标准的 `role: "tool"` + `ToolCallID`。同一操作产生不同结构的消息，会污染对话历史。

**修复方向**: Flash 路径也使用 `role: "tool"` + `ToolCallID`。

---

### 14. `model/device.go` + `controller/devices.go` — 整组死代码

**问题**: `registerDevice` 函数从未在 router 注册。整个 device-centric 模型已迁移到 user-centric，遗留代码未清理。

**修复方向**: 删除 `devices.go`、`model/device.go`、`service/device_repo.go`。

---

### 15. `llm/deepseek.go` — `parseDeepSeekResponse` 返回未使用的值

**行号**: 290

**问题**: 返回 `Message`（已内含 `ReasoningContent` + `ToolCalls`），还单独返回 `reasoning string` + `toolCalls []ToolCall`。所有 caller 都丢弃了后两者。

**修复方向**: 简化签名，只返回 `(Message, error)`。

---

### 16. `config/config.go` — `RegistrationEnabled` bool 合并 bug

**行号**: 115

**问题**:
```go
if src.Server.RegistrationEnabled {  // false 是零值，永远不会覆盖 true
```
环境特定配置**无法**把 `RegistrationEnabled` 从 true 改成 false。

**修复方向**: 改用 `*bool` 指针类型，或显式区分"未设置"和"设置为 false"。

---

### 17. 缺少复合索引和唯一约束

**问题**:
- `UserMemory(user_id, memory_key)` — 无 UNIQUE 约束，并发场景可能插入重复记忆；`First()` 返回不确定的行
- `Message(conversation_id, created_at)` — 无复合索引导致排序需要 filesort
- `Note` 的 `Topic` 单列索引浪费（从不单独查询 topic，总是配合 user_id）

**修复方向**: 补充对应的 GORM index tag。

---

### 18. `controller/chat.go` — 错误被静默吞掉

**行号**: 34, 44

**问题**: `convRepo.FindByID` 和 `msgRepo.ListByConversation` 的错误直接忽略（`_`），数据库故障会静默创建新会话或返空历史。

**修复方向**: 至少 log 错误，合理时向上传递。

---

### 19. `tool/bookkeeping.go` — `json.Unmarshal` 错误被忽略

**行号**: 289, 321, 362

**问题**: 三个函数收到格式错误的 JSON args 时，`json.Unmarshal` 错误直接 `_` 忽略，静默使用零值继续执行。

**修复方向**: 返回 `&Result{Success: false, Error: "parse args: " + err.Error()}`。

---

### 20. `tool/registry.go` — `GeneratePrompt` 中无守卫的类型断言可能 panic

**行号**: 70, 74

**问题**:
```go
required[r.(string)] = true           // r 不是 string → panic
prop := val.(map[string]interface{})   // prop 不是 map → panic
```
若工具返回格式异常的参数 schema，在生成 tool prompt 时会使整个服务器 crash。

**修复方向**: 加 ok 守卫。

---

## P3 — Low (7 项)

### 21. `llm/claude.go` — 整个文件是未实现的 stub

**问题**: `Chat` 和 `ChatSync` 都返回 "not implemented yet"，`BuildSystemPrompt` 忽略 tools 参数。如不用 Claude 可删除，否则应实现。

---

### 22. `llm/doubao.go` — 重复定义 `func min(a, b int) int`

**行号**: 197

**问题**: Go 1.21+ 已有 builtin `min`，自定义版本多余。与较早的 deepseek.go 修复类似。

---

### 23. `model/bill.go` — `Date` 用 string 而非 `time.Time`

**行号**: 32

**问题**: 存为 `YYYY-MM-DD` 字符串无法使用 MySQL 的 `MONTH()`、`YEAR()` 等日期函数，也无法做索引范围扫描。

---

### 24. `model/bill.go` — `Amount` 用 `float64` 存金额

**行号**: 28

**问题**: 金融数据应使用整数分（`int64`）避免浮点精度问题（如 `0.1 + 0.2 != 0.3`）。

---

### 25. `cmd/xhan-helper/main.go` — 关闭时后台 goroutine 被遗弃

**行号**: 84, 89

**问题**: `Warmup` 和 `MemoryScheduler` 启动的 goroutine 没有 context 取消，`srv.Shutdown` 只停 HTTP server，这两者会被系统强制终止。

---

### 26. `controller/conversations.go` — 所有权检查样板代码重复 3 次

**问题**: `getMessages`、`renameConversation`、`deleteConversation` 各有相同的 6 行授权逻辑，应提取为辅助函数。

---

### 27. `service/message_repo.go` — `GetByID` 不遵循 `(nil, nil)` 约定

**行号**: 34-40

**问题**: 同项目中其他 repo 的 `FindBy*` 对 record-not-found 返回 `(nil, nil)`，但 `MessageRepo.GetByID` 直接透传 `gorm.ErrRecordNotFound`，调用方需额外处理。

---

## 统计

| 优先级 | 数量 | 关键主题 |
|--------|------|----------|
| P0 Critical | 3 | PlanExecutor 不可用、跨用户数据访问、CORS |
| P1 High | 7 | 写入语义错误、goroutine 泄漏、GORM 关系断裂、非事务性写入 |
| P2 Medium | 10 | God struct、参数过多、role 不一致、死代码、配置 bug、缺索引 |
| P3 Low | 7 | Stub、重复函数、类型选择、关闭不优雅、样板代码 |
