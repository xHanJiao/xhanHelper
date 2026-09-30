# 文档批注 · AI 回复（多轮）

> 状态：**客户端 UI 已实现（P1 只读侧 + Word 式连线）；服务端待实现**（2026-09-30）
> 分支：`feat/document-annotation-reply`
> 范围：server（Go：模型 + repo + API + LLM 工具 + 生成器）+ macOS 客户端（SwiftUI / WKWebView）
> 前序：`docs/design/document-highlight-annotation/design.md`（划线与批注本体，v1 已上线）
> 唯一入口：macOS 客户端。iOS / Web / 分享页 **不做**（延续 D1）。
>
> **当前落地进度**（下次 compact 后先看这里）：
> - ✅ **服务端已实现并 `go build ./...` + `go test ./...` 全绿**：`DocumentAnnotationReply` 表 / repo / `GET :id/replies` / `POST :id/reply` / 生成器（`agent/annotation_reply.go`）/ `list_annotations` 带 replies / `reply_annotation` 工具 / 删批注与文档 purge 级联。见 §三/§四/§五/§六。
> - ✅ **客户端 UI 已写完并 `swiftc -typecheck` 0 error**：常驻批注面板（讨论/全部两视图）、可折叠线程卡片、收放规则、卡片→划线连线、`#Preview`（桌面三栏）。见 §7。
> - ✅ 客户端模型/接口已就位：`AnnotationReply`、`reply_count`/`last_reply`（可选）、`APIClient.listAnnotationReplies/replyToAnnotation`。
> - ✅ 阅读器 JS 已加 `annotationRect(id)` + `scroll` 消息（供连线）。
> - ⏳ **只剩「问 AI」全套未拆**（纯客户端：工具条/菜单入口、`DocumentAIBridge.onAsk`、`ChatView` 胶囊/窄条、`ChatViewModel.docQuote`、`DocQuote.swift`）。
> - ⏳ `DELETE /api/annotation-replies/:id` 未做（v1 用不到，接口可后置）。
> - ⏳ 真机验收（§11）未跑；`check-reader-html.sh` 未加连线专属断言。

---

## 一、背景与目标

现状：文档支持**划线 + 批注**（`DocumentAnnotation`），LLM 能通过 `list_annotations` 只读查看用户的划线与批注。P5 的「问 AI」是**会话级引用 + 主聊天**：选中一段 → 聊天栏出引用胶囊 → 用户手动问 → 回答可「存为批注」。实际使用中「问 AI」基本不用。

本次目标：

1. **LLM 针对批注给出回复**，回复与批注**绑定、落库、可回看**。
2. **多轮**：用户可对 AI 回复继续追问，形成一条挂在批注下的讨论线程。
3. **两个触发源**：
   - 用户直接对 agent 说「看下批注，逐条回复」（在主聊天里，agent 批量回）。
   - 批注上点「**让 AI 回复**」按钮（**替换掉「问 AI」**）。
4. 「问 AI」那套（会话级 `docQuote` + 引用胶囊 + 窄条「存为批注」）**整体下线**。

---

## 二、决策记录

| 编号 | 决策 | 结论 |
|---|---|---|
| AR-1 | 数据模型 | ✅ **新表 `DocumentAnnotationReply`**。**不**在批注表加 `parent_id` 自关联（理由见 §3.3） |
| AR-2 | 多轮的语义 | ✅ 「多轮」= 多次**相互独立**的 run；每次用户动作 = 一次 ReAct run = 一条 `assistant`。**不是**活的自主循环（详见 §4.4） |
| AR-3 | 生成机制 | ✅ 统一走**工具型 agent**（`agent.MiniReAct`）。两条触发路径只是「宿主 + 上下文种子」不同 |
| AR-4 | 按钮路径的宿主 | ✅ **document-scoped MiniReAct**，**不进当前会话**。同步 POST + spinner（无 SSE） |
| AR-5 | 指令路径的宿主 | ✅ 主聊天（当前会话），agent 显式调 `reply_annotation` 工具落库 |
| AR-6 | 上下文锚定 | ✅ 以 **`Document.ConversationID`** 定位「产出该文档的会话」，读它的**摘要 + 近期轮**当参考。**不**做 session window 定位（脆弱、边界多，用户否掉） |
| AR-7 | run 落点 | ✅ 默认**纯内存**（不注入 `MsgSink`）→ 不写任何会话。可选挂一个 **hidden 子会话**做 trace（见 §8） |
| AR-8 | 线程存储 | ✅ **α：回复表为准** + 可选 trace 子会话（β「一条批注=一个子会话」被否，理由见 §3.4） |
| AR-9 | 批注根与回复 | ✅ 根仍是 `DocumentAnnotation.note`；追问与 AI 回复是 `DocumentAnnotationReply` 行 |
| AR-10 | 工具白名单 | ✅ run 内只给**只读**工具（`document` / `unified_search` / `message_search` 等），杜绝 run 顺手改会话/业务状态 |
| AR-11 | 记忆/摘要隔离 | ✅ 批注与回复**不进**记忆 / 画像 / 摘要流水线（延续前序 D13）；run 不触发 `PostChatWorkerGroup` |
| AR-12 | 「问 AI」 | ❌ 删除：工具条入口、点击菜单入口、`DocumentAIBridge.onAsk`、`ChatView` 引用胶囊/窄条、`ChatViewModel.docQuote` |
| AR-13 | 失败处理 | ✅ 生成失败只影响本次回复；错误向上抛、顶层记一次日志、返回错误给客户端（不吞） |
| AR-14 | 客户端 UI 形态 | ✅ **常驻批注面板**（Word 批注窗格式），两视图：**讨论**（卡片 + 可折叠线程）/ **全部**（扁平一级列表，原三 tab）。收放见 §7.3 |
| AR-15 | 卡片 → 划线连线 | ✅ 悬停/操作卡片时从卡片左缘拉一条细直线到正文划线（Word 式）。色号 = 划线高亮的 `--hl`，**不是** `appAccent`。见 §7.4 |

---

## 三、数据模型

### 3.1 新表 `DocumentAnnotationReply`（`server/internal/model/document_annotation_reply.go`）

```go
package model

import "time"

// DocumentAnnotationReply — 文档批注下的讨论消息（用户追问 / AI 回复）。
// 根批注在 DocumentAnnotation.note；本表只放批注之后的往来。
// 回复没有锚点语义，与批注分开存，避免污染 RelocateAll / 配额 / list_annotations。
type DocumentAnnotationReply struct {
    ID           string `gorm:"primaryKey;size:36" json:"id"`
    AnnotationID string `gorm:"index;size:36;not null" json:"annotation_id"`
    // DocumentID 冗余：批注不会换文档，关系不可变；换来文档级级联删 / 按文档批量取，免 JOIN。
    DocumentID string `gorm:"index;size:36;not null" json:"document_id"`
    UserID     string `gorm:"index;size:36;not null" json:"user_id"`
    // Role 角色：user（用户追问）| assistant（AI 回复）
    Role    string `gorm:"size:20;not null" json:"role"`
    Content string `gorm:"type:text;not null" json:"content"`
    // Source 来源（可选，便于以后筛选）：button | chat | followup
    Source    string    `gorm:"size:20" json:"source"`
    CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
```

- 排序 `created_at ASC, id ASC`。
- 索引：`annotation_id`、`document_id`、`user_id`。
- 不做软删（与 `Document` / `DocumentAnnotation` 一致，物理删）。
- 不加 GORM `belongs to`（项目惯例，靠 FK 手 JOIN）。

### 3.2 批注表不改

`DocumentAnnotation` 保持原样。根 `note` 仍是批注正文（可 PATCH 编辑）；回复独立成行。

### 3.3 为什么**不**用 `parent_id` 自关联

批注行**带锚点语义**（`quote not null` + prefix/suffix/offset），正文一变 `RelocateAll` 会遍历该文档**全部行**重定位。回复行没有锚点，若同表，则每条锚点路径都要补 `parent_id IS NULL` 过滤，漏一处即 bug：

| 触碰点 | 不加过滤的症状 |
|---|---|
| `document_annotation_repo.go:110` `RelocateAll` | 回复行被当批注重定位 → 批量标 orphan / 改写 |
| `document_annotation_repo.go:37` `List` | 回复混进批注列表 |
| `document_annotation_repo.go:58` `CountByDocument` | 回复占满 500 条配额 |
| `tool/document.go:596` `listAnnotations` | LLM 把回复当「用户的批注」读回来 |

而且 `parent_id` 不表达「谁写的」，还得加 `role` 列 —— 等于两列 + 一个实体拆成两种语义。**拆表后上述路径一行不用改。**

### 3.4 为什么是 α（回复表为准）而不是 β（一条批注 = 一个 hidden 子会话）

| | α（采用） | β（否） |
|---|---|---|
| 线程存储 | `DocumentAnnotationReply` | 会话消息（`Conversation.ParentID` + `Message`） |
| 展示 / 下发 | ✅ 一次 `WHERE annotation_id IN (?)` 批量 | 需过滤 `role∈{user,assistant}` 藏 tool + 一次 join |
| 根 note | ✅ 单一来源（批注表） | 批注 note + 合成首条 user **双份**，需冻结取舍 |
| 工具/推理痕迹 | 在可选 trace 子会话（按需接） | 混在线程里，展示必须过滤（易漏） |
| 域边界 | ✅ 批注子结构，域干净 | 复用会话域，Annotation↔Conversation 耦合，Admin 子会话列表混入 |
| 重放保真 | 不白送（可显式接 trace / LLM I/O 审计） | 白送 |
| 并发串行 | 需自建按 annotation 锁 | 复用 `lockSubAgent` |

结论：用户可见的线程要干净、批注是文档域子结构 → **α**。β 唯一实质优势（逐轮输入留痕、直接 replay）用 α 的**可选 trace 子会话**即可补，且可关。

---

## 四、生成机制

### 4.1 组件：`MiniReAct`（`server/internal/agent/mini_react.go`）

- 同步 `ChatSync` + 原生 function calling，工具结果以 `role=tool` 回填。
- **持久化是可选的**：只有注入 `SubAgentExt.MsgSink` 才会落库（`mini_react.go:198/244/288/386`）。`nil` = 纯内存（`profile_worker` / `record_loop` 现状）。
- 种子历史 `InitialMessages` 注释明写「**不重复落库**」（`mini_react.go:36`）——把原会话摘要/近期轮当上下文是**只读**。
- `ConvID` 注入 `ExecuteContext` 只是给工具用，本身不落消息；`ToolExposure` 那类写 `Conversation.Extra` 的逻辑只在主编排循环，MiniReAct 没有。

### 4.2 新增 `server/internal/agent/annotation_reply.go`

职责：给定 `(userID, annotationID, userContent?)`，跑一次 run，产出并落库一条 `assistant` 回复。
实现：`type AnnotationReplier struct{...}` + `NewAnnotationReplier(cfg)` + `Generate(ctx, userID, annotationID, userContent string) (*AnnotationReplyOutcome, error)`。

```
Generate:
  0. tryLock(annotationID) 非阻塞 → 拿不到 = ErrAnnotationReplyBusy
  1. 载入批注 + 文档（校验归属；取不到 = ErrAnnotationReplyNotFound）
  2. 回复数 ≥ maxReplies → ErrAnnotationReplyLimit
  3. 载入线程（≤200）+ 组装上下文（§4.3）
  4. NewMiniReAct(cfg) 跑一次（只读工具白名单，超时 120s，6 轮）；**不注入 MsgSink**
  5. 取最终 assistant 文本 → 截断 ≤4000 → 与（可选）user 追问行**一次 CreateBatch 落库**（原子）
  6. 返回 Added（含追问）+ ReplyCount
```

失败：busy/limit/notfound 走哨兵错误（handler 映 409/400/404）；生成错误向上抛 → 502；落库错误包 `ErrAnnotationReplySave` → 500。`500/502` 在 handler 顶层 `log.Printf` 一次；**不吞**。

### 4.3 上下文降级链（`loadOriginContext`）

顺序即优先级，前一级拿得到就不必更差的一级：

1. **原点会话（参考）** — 若 `doc.ConversationID != ""`：
   - 取该会话的 **active `ConversationSummary`**（`ConversationSummaryRepo`），覆盖老历史。
   - 取该会话**最近 N 轮消息**（复用 `MessageRepo.ListRecentRounds` 那套，N 待定，如 10）。
   - **无视 `is_deleted`**（归档不拦）。
   - 读不到（无痕已销毁 / 无该会话）→ 跳过。
2. **文档正文** — 标题 + 相关窗口（长文用 `get_document` 的窗口读；run 内可自行调）。
3. **批注 + 线程** — `quote` / `note` + 已有 `replies`。
4. **工具兜底** — run 内可调 `document` / `unified_search` / `message_search` 自行补。

> 定位到「会话」而非「session window」是刻意取舍（AR-6）：窗口切分边界多、脆弱，用户明确否掉。会话级摘要 + 近期轮已是聊天本来就给的量。

### 4.4 单轮语义（重要）

```
用户发一条（或点一次按钮）
  → ReAct 跑一次（内部可能多次 LLM↔工具往返）
  → 产出一条 assistant 回复
  → 落线程
```

- 用户侧「多轮」= 线程里累积多组 `user → assistant`，**每组各由一次独立 run 生成**。无后台自主续聊、无跨 run 活状态。
- 每次 run 的上下文 = 线程历史 + 原点会话摘要/近期轮 + 文档 + 批注 + 工具。run 之间无状态，状态全在线程里。
- 与聊天一轮等价（一次 user 消息 = 一次 ReAct run）。所以 **v1 同步 + spinner** 成立：一次 POST 跑完一次 run。

### 4.5 两条触发路径

| 路径 | 宿主 | 落库方式 | 备注 |
|---|---|---|---|
| **按钮**「让 AI 回复」 | document-scoped MiniReAct（不进会话） | §4.2 自动 append（不依赖 agent 调工具） | 同步 POST + spinner；`Source=button` |
| **指令**「看下批注逐条回复」 | 主聊天（当前会话） | agent 显式调 `reply_annotation(annotation_id, content)` | 富上下文（会话历史 + 工具）；`Source=chat` |

- 批量路径「逐条回复」必须用显式工具：一轮里给多条批注回复，自动 append 无法对应到哪条。
- 单条路径（按钮/追问）用自动 append：不依赖 agent 自觉，可靠。

---

## 五、服务端 API

统一约定（先看这里）：

- 成功 `{"status":"ok","data":{...}}`；失败 `{"status":"error","error_code":N,"error_msg":"..."}`（`view.JSONOK` / `view.JSONError`）。
- 全部按 `user_id` 过滤；**越权一律 404**（不区分「不存在」与「不是你的」）。
- 分页一律 `limit/offset`；`created_at` 为 Go `time.Time` 默认 RFC3339，客户端按 `String` 解码（与现有批注一致）。
- 回复对象 `<Reply>`：

```jsonc
{
  "id": "uuid", "annotation_id": "uuid", "document_id": "uuid", "user_id": "uuid",
  "role": "user" | "assistant",
  "content": "...",
  "source": "button" | "followup" | "chat",   // 生成来源，便于筛选/排查
  "created_at": "2026-09-30T12:05:00+08:00"
}
```

路由总览：

| 方法 | 路径 | 状态 |
|---|---|---|
| `GET` | `/api/documents/:id/annotations?limit=&offset=` | **改**：每条加 `reply_count` + `last_reply` 摘要 |
| `GET` | `/api/annotations/:id/replies?limit=&offset=` | **新**：拉完整线程 |
| `POST` | `/api/annotations/:id/reply` | **新**：生成回复（可选带追问），同步 |
| `DELETE` | `/api/annotation-replies/:id` | **新（可后置）**：删单条回复 |

### 5.1 `GET /api/documents/:id/annotations`（改：加回复摘要）

Query：`limit`(默认 200) / `offset`(默认 0)。Response 在每条批注对象上**追加两个字段**，其余不动：

```jsonc
{
  "status": "ok",
  "data": {
    "annotations": [
      {
        "id": "...", "document_id": "...", "quote": "...", "prefix": "...", "suffix": "...",
        "note": "...", "start_offset": 3, "end_offset": 78,
        "orphaned": false, "quote_rewritten": false,
        "created_at": "2026-09-30T12:00:00+08:00", "updated_at": "...",

        "reply_count": 3,           // 新增，int
        "last_reply": {             // 新增，<Reply>|null（最新一条，供折叠行预览）
          "id": "...", "annotation_id": "...", "document_id": "...", "user_id": "...",
          "role": "assistant", "content": "...", "source": "button",
          "created_at": "2026-09-30T12:05:00+08:00"
        }
      }
    ],
    "total": 42
  }
}
```

- **不内嵌完整线程**：500 批注 × 最多 100 回复会把列表撑爆。摘要字段两次批量查算出，**无 N+1**：
  - `reply_count`：`WHERE annotation_id IN (?) GROUP BY annotation_id`
  - `last_reply`：一次 `WHERE annotation_id IN (?) ORDER BY created_at DESC, id DESC`，内存按 annotation 取第一条
- 完整线程走 §5.2，抽屉展开时按需拉。

### 5.2 `GET /api/annotations/:id/replies`（新：线程）

Query：`limit`(默认 100，上限 200) / `offset`(默认 0)。按 `created_at ASC, id ASC`：

```jsonc
{
  "status": "ok",
  "data": { "replies": [ <Reply>... ], "total": 3 }
}
```

- 只回**回复**，**不含**根批注；根是 `annotation.note`，客户端自己拼。
- 404：批注不存在 / 非本人。

### 5.3 `POST /api/annotations/:id/reply`（新：生成 / 追问）

Body（`content` 可省/空 = 纯按钮生成）：

```jsonc
{ "content": "那听谁的？" }   // 可选；有值 = 先作为 role=user 追问
```

Response（同步；生成完成后一次性返回增量）：

```jsonc
{
  "status": "ok",
  "data": {
    "added": [                                   // 按序，供客户端 append
      { "id":"...", "role":"user",      "content":"那听谁的？", "source":"followup", "created_at":"..." },
      { "id":"...", "role":"assistant", "content":"...",       "source":"followup", "created_at":"..." }
    ],
    "reply_count": 5                             // 该批注回复总数（更新折叠行）
  }
}
```

- **原子性**：只有生成成功才把「user 追问 + assistant 回复」**一起**落库。生成失败 → **什么都不落**（可安全重试）。
- 无 `content` 时 `added` 只含一条 `assistant`（`source:"button"`）；有 `content` 时两条（`source:"followup"`）。
- **并发**：同一批注并发请求用按 `annotation_id` 的进程内 keyed mutex 串行；拿不到锁 → 409。防双击 / 双端同时点。
- **超时**：生成用带超时的 context（建议 120s），超时按失败处理。

错误：

| code | 场景 | `error_msg` |
|---|---|---|
| 400 | `content` > 4000 字 | `追问过长` |
| 400 | 回复数 ≥ 100 | `该批注回复数量已达上限` |
| 404 | 批注不存在 / 非本人 | `划线与批注不存在` |
| 409 | 该批注正在生成 | `该批注正在生成回复，请稍候` |
| 502 | 生成失败 / 超时 | `生成回复失败` |
| 500 | 落库失败 | `保存回复失败` |

（`502/500` 顶层 `log.Printf` 记原始 error 一次；内部逐层 `fmt.Errorf("annotation reply: %w", err)` 加语境。）

### 5.4 `DELETE /api/annotation-replies/:id`（新，可后置）

Response：`{"status":"ok","data":{"deleted":true}}`；404：不存在 / 非本人。

### 5.5 工具 `list_annotations`（改：带回复）

返回项追加 `reply_count` + `replies`（**截断**，控 token）：

```jsonc
{
  "annotations": [
    {
      "quote": "...", "note": "...", "orphaned": false, "created_at": "2026-09-30 12:00",
      "reply_count": 3,
      "replies": [                                  // 最近 ≤20 条，升序
        { "role": "user", "content": "...", "created_at": "2026-09-30 12:04" },
        { "role": "assistant", "content": "...", "created_at": "2026-09-30 12:05" }
      ],
      "replies_truncated": false                    // true = 有更早的未返回
    }
  ],
  "total": 42,
  "hint": "note 为空=只划线；orphaned=true=原文已改写、划线失活；replies 是该批注下已有讨论"
}
```

### 5.6 工具 `reply_annotation`（新：写，仅批量路径）

```
reply_annotation(annotation_id: string, content: string)   // content ≤ 4000
```

- 落库 `role:"assistant"`, `source:"chat"`。
- 校验：批注归属 / 文档存在 / `content` 长度 / 回复数上限；违规返回 `{success:false, error:"..."}`。
- 成功：`{success:true, data:{id, annotation_id, created_at}}`。
- 同步维护 `Functions()` / `ReadOnlyFuncs()`（**不**列入 → 视为写）/ `Catalog()`；不实现 `CatalogProvider` 或不同步，注册表启动校验会 panic。
- `ReadOnlyFuncs` 现含：`get_document` / `list_annotations` / `query_documents` / `list_documents` / `list_tags`（`document.go:307`）。

---

## 六、服务端触碰点

> ✅ 以下均已落地（`go build ./... && go test ./...` 全绿）。

| 文件 | 动作 | 状态 |
|---|---|---|
| `server/internal/model/document_annotation_reply.go` | **新** 模型 | ✅ |
| `server/internal/service/document_annotation_reply_repo.go` | **新**：`CreateBatch` / `ListByAnnotationID` / `CountByAnnotation` / `CountsByAnnotationIDs` / `LatestByAnnotationIDs` / `ListRecentByAnnotationIDs`（窗口函数取每批注最近 N）/ `DeleteByAnnotation` / `DeleteByDocument` | ✅ |
| `server/internal/agent/annotation_reply.go` | **新** `AnnotationReplier`：上下文组装（原点会话摘要+近期轮 → 文档 → 线程 → 工具）+ MiniReAct run + 原子落库；按 annotation 的 `tryKeyedMutex`（拿不到→`ErrAnnotationReplyBusy`） | ✅ |
| `server/internal/app/init_db.go` | AutoMigrate `&model.DocumentAnnotationReply{}`（紧挨 `DocumentAnnotation`） | ✅ |
| `server/internal/app/init_repos.go` | 注册 `DocumentAnnotationReply` repo | ✅ |
| `server/internal/appinit/router.go` | 构造 `agent.NewAnnotationReplier(...)` 并注入 `AnnotationHandler` | ✅ |
| `server/internal/controller/router.go` | 加 `GET /annotations/:id/replies`、`POST /annotations/:id/reply` | ✅ |
| `server/internal/controller/annotation.go` | `ListAnnotations` 加 `reply_count`/`last_reply`（两次批量查）；新增 `ListReplies` / `ReplyAnnotation`；`DeleteAnnotation` 级联删回复；常量 `maxAnnotationReplyLen`(4000) / `maxRepliesPageSize`(200) | ✅ |
| `server/internal/service/document_repo.go` | `purge` 事务内**一并删回复**（★实际级联点在 repo 的物理清理，不在 controller；软删时批注/回复都留到 purge） | ✅ |
| `server/internal/tool/document.go` | `listAnnotations` 带 `replies`+`reply_count`+`replies_truncated`；新增 `reply_annotation`（写，`source=chat`）；`NewDocumentTool` 加 reply repo 依赖 | ✅ |

**不改**：`document_annotation_repo.go`（`RelocateAll` / `List` / `CountByDocument` 全不动）、`document_anchor.go`。`ReadOnlyFuncs` **不含** `reply_annotation`（视为写），`Catalog` 不动。

---

## 七、客户端 UI（macOS）—— 已实现

> 详细决策见「二、决策记录」AR-14 / AR-15。本节是**已落地的实现说明**（自包含，compact 后照此可继续）。

### 7.1 面板两视图（AR-14）

`AnnotationPanel`（`Shared/Views/AnnotationDrawer.swift`）：

- **讨论视图**（`AnnotationPanelMode.thread`，默认，常驻）：`AnnotationThreadCard` 列表——每条批注 = 划线圈引 + 批注 + 可折叠线程。
- **全部视图**（`.list`）：`AnnotationListBody`——原扁平抽屉（三 tab：全部 / 仅批注 / 已失活），纯导航、无线程。
- 头部：`划线与批注 N` + 讨论/全部两个图标按钮 + 收起 chevron。面板宽 `Layout.docAnnotPanelWidth = 300`。

### 7.2 线程卡片交互（`AnnotationThreadCard`）

- **折叠态**：`回复 N · <最后一句>` + chevron；`N == 0` 时显示「让 AI 回复」按钮（`sparkles`）。
- **展开态**：逐条 user / assistant 行（左竖条着色）+ 底部输入框：
  - 空提交 → 「让 AI 回复」（`POST` 无 `content`，`source=button`）；
  - 有文本 → 追问（`POST` 带 `content`，先落 `role=user` 再生成，`source=followup`）。
- **数据来源**：折叠摘要用列表接口带的 `reply_count` / `last_reply`；展开时按需 `GET /api/annotations/:id/replies`（控制器缓存 `repliesByAnnotation`）。
- **状态**：`expandedAnnotationIDs` / `loadingReplyIDs` / `busyReplyIDs`（控制器），卡片据 `busy` 显示 spinner 并禁用输入。
- 失活批注：灰态 + `已失活` 徽标；划线圈引点击 → `scrollToAnnotation`。

### 7.3 收放规则（AR-14 / c2：手动优先 + 兜底）

- **全局记忆**（`@AppStorage`，跨文档跨启动）：`xhan_doc_annot_open`（默认 **true**）、`xhan_doc_annot_mode`（默认 `thread`）、`xhan_doc_toc_collapsed`（默认 **true**，**目录默认收起**）。
- **有效态 = 手动偏好 ∧ 窗口宽 ≥ 硬地板**（手动优先，只在窗口窄到触发硬地板时强制收起）。
- **先收目录、再收批注**：`Layout.docTocHardFloor = 780` > `Layout.docAnnotHardFloor = 560`
  （= `docReaderMinWidth 260 + docAnnotPanelWidth 300 [+ docTocExpandedWidth 220]`，都在 `Shared/Views/LayoutConstants.swift`）。
- 宽度用 `onGeometryChange { $0.size.width }` 量文档 tab 内容宽（`DocumentFullView`）。
- 收起态 = **窄条**（`Layout.docPanelStripWidth = 32`，`chevron.left` + 批注数角标）；面板头部 chevron / 顶栏按钮控制开合。

### 7.4 卡片 → 划线连线（AR-15）

- **JS**（`DocumentReaderView.swift` 模板）：
  - `reader.annotationRect(id)` → 该划线**首个 `mark` 的首个行盒**的视口矩形 `{x,y,w,h}`（左上原点），找不到返回 `null`；
  - `scroll` 监听补发 `{type:'scroll'}`（`popover` 内滚不算）。
- **控制器**：`connectorID` / `connectorRect`（阅读器内坐标）+ `beginConnectorTracking` / `endConnectorTracking` / `measureConnector`；收到 `scroll`、或 `pushAnnotations()` 重画后自动重测。
- **换算**（关键，别想复杂）：`getBoundingClientRect` 与 SwiftUI `.global` 都是「左上原点、y 向下」，**直接相加**——终点 = `readerFrame.origin + connectorRect`（`readerFrame` 由 `DocumentReaderView.onGeometryChange { $0.frame(in: .global) }` 量得）。**不碰 AppKit 的翻转坐标系**。
- **起点** = 卡片左缘中点（`AnnotationThreadCard.onHover` 上报 `(id, inside, global frame)`）。
- **画**：`AnnotationConnectorLayer`（跨平台定义，iOS 端点恒 nil）——在文档 tab `HStack` 上 `.overlay`，1pt 直线、圆头、`allowsHitTesting(false)`；划线滚出视口则不画。
- **色号**：`AnnotationHighlightColor.value(colorScheme)` = 阅读器 `mark.hl` 的 `--hl`（浅 `rgba(217,151,27,1)` / 深 `rgba(146,200,180,.9)`）。**不要改成 `appAccent`**；连线颜色必须与划线一致。

### 7.5 客户端触碰点（现状）

| 文件 | 动作 | 状态 |
|---|---|---|
| `Shared/Models/DocumentAnnotation.swift` | `AnnotationReply` 模型；`DocumentAnnotation.replyCount: Int?` / `lastReply: AnnotationReply?`（**可选**，老服务端不带也不炸）；`AnnotationReplyListResponse` / `AnnotationReplyCreateResponse` | ✅ |
| `Shared/Services/APIClient.swift` | `listAnnotationReplies(annotationId:limit:offset:)`、`replyToAnnotation(id:content:)` | ✅ |
| `Shared/Views/DocumentReaderView.swift` | 控制器线程缓存 + 展开/载入/生成态 + `toggleThread/loadReplies/reply/forgetThread`；连线追踪；JS `annotationRect` + `scroll` 消息 | ✅ |
| `Shared/Views/AnnotationDrawer.swift` | `AnnotationPanel` / `AnnotationThreadCard` / `AnnotationListBody` / `AnnotationConnectorLayer` / `AnnotationHighlightColor` + 桌面预览 | ✅ |
| `Shared/Views/DocumentListView.swift` | 常驻面板、`@AppStorage` 三键、`onGeometryChange` 收放、悬停状态 + 连线 overlay | ✅ |
| `Shared/Views/LayoutConstants.swift` | 面板/窄条/目录宽、`docReaderMinWidth`、两个硬地板 | ✅ |
| `Shared/Views/DocumentReaderView.swift`（JS 工具条/菜单） | **删「问 AI」**：`data-act="ask"`、`data-cact="ask"`、`ask` case、`revealQuote` / `beginNoteFromAI` 入口 | ⏳ P3 |
| `Shared/Services/DocumentAIBridge.swift` | `onAsk` / `weak reader` 拆 | ⏳ P3 |
| `Shared/Views/ChatView.swift` | 拆「引用胶囊」+「存为批注」窄条（`:161-170`、`:383` 附近）+ `storeQuoteAsNote` | ⏳ P3 |
| `Shared/ViewModels/ChatViewModel.swift` | 拆 `docQuote` / `attachDocQuote` / `quoteAnswerText`（`:179-194`） | ⏳ P3 |
| `Shared/Models/DocQuote.swift` | 拆完「问 AI」后若无人用可删 | ⏳ P3 |

### 7.6 自查

- `#Preview`（桌面三栏 shell，1200×720）：`AnnotationDrawer.swift` 末尾 6 个 + `连线 · 悬停卡片`（用**底部波浪线** mock 划线，非底色高亮）。
- 自检命令：`swiftc -typecheck -D DEBUG`（macOS / iOS 两端 0 error，见 §11）；`client/xHelper/scripts/check-reader-html.sh`（**179 条断言 PASS**）。
- 真机待验：悬停出线 / 移开消失 / 滚动跟随 / 线色与划线贴合；收放窄条切换。

---

## 八、run 落点与生命周期

### 8.1 默认：纯内存

- 不注入 `MsgSink` → run 的 ReAct 消息不进任何会话；原会话历史/摘要/记忆流水线**全不动**。
- 唯一写入是 `DocumentAnnotationReply` 行（设计意图）。
- 保险：`ConvID` 传空 + 只读工具白名单，杜绝任何工具顺手改会话侧状态。
- 即便将来注入 sink，按 `RunSubAgent` 的模式也只会写进 **hidden 子会话**（`IsSubAgent=true`），不会写进原会话。

### 8.2 可选：trace 子会话

- 需要 replay / 调试时，仿 `RunSubAgent` 建一个 hidden 子会话：`ParentID = doc.ConversationID`，`Extra.SubAgentType = "annotation_reply"`（或新 Extra 字段以区分），注入 `MsgSink` 写完整 run。
- 一条批注线程**复用一个**子会话，多次 run 累积（仿 subagent resume，`subagent.go:489-530`）。
- `parent_id IS NULL` 过滤（`conversation_repo.go:88`）保证不进用户会话列表；`IsSubAgent=true` 保证不进检索聚合。
- 文档无 `ConversationID` 时无父可挂 → 保持纯内存。
- **v1 可不做**，留接口。

### 8.3 批注 / 文档删除

- 删批注：删该 `annotation_id` 的全部回复（+ 若接了 trace，删子会话及其消息）。
- 删文档：`DeleteByDocument` 删批注，再 `DeleteByDocument` 删回复（失败只记日志不阻断）。
- 无痕会话：批注跟文档走，无独立生命周期（延续前序）。

---

## 九、边界与配额

| 项 | 值 | 校验位置 |
|---|---|---|
| 单条回复 / 追问 `content` | ≤ 4000 字 | 服务端（超 `400`）+ 客户端输入上限 |
| 单批注回复条数 | ≤ 100（达上限 `POST /reply` 返 400） | 服务端 |
| 线程接口分页 | `limit` 默认 100、上限 200 | 服务端 |
| `list_annotations` 回带回复 | 每批注最近 ≤20 条 | 服务端 |
| 生成上下文：原点会话近期轮 | N 轮（如 10） | 服务端 |
| 生成上下文：会话摘要 | 1 段 active | 服务端 |
| 越权 | 404 | 服务端 |

阈值写死（`ponytail:` 等真出现误伤再暴露配置）。

---

## 十、分期

| 阶段 | 内容 | 状态 |
|---|---|---|
| **P0 客户端 UI** | 常驻面板 + 讨论/全部两视图 + 线程卡片 + 收放规则 + 卡片→划线连线 + 预览 | ✅ **已完成**（`swiftc -typecheck` 0 error） |
| **P1 数据 + 读（服务端）** | 表 + repo + 级联删 + `list_annotations` 带 replies + 列表 API 带 `reply_count`/`last_reply` + `GET replies` 线程接口 | ✅ **已完成**（含单测） |
| **P2 按钮生成（服务端）** | `agent/annotation_reply.go`（上下文链 + MiniReAct）+ `POST /reply`；客户端已就绪（§7） | ✅ **已完成** |
| **P3 批量 + 清理** | `reply_annotation` 工具（指令式逐条）✅；拆「问 AI」全套（客户端）⏳ | 🟡 **服务端完成，客户端待拆** |

依赖：P2 依赖 P1；P3 依赖 P1/P2。
服务端（P1+P2+P3 的 Go 部分）已全部落地，`go build ./... && go test ./...` 全绿；客户端只剩「问 AI」拆除。

---

## 十一、验收

0. **P0（客户端 UI，已完成）**：
   - `swiftc -typecheck -D DEBUG`（macOS / iOS）0 error；`check-reader-html.sh` 179 断言 PASS。
   - Xcode 画布：`AnnotationDrawer.swift` 的 6 个桌面预览 + `连线 · 悬停卡片` 观感正确。
   - 真机：常驻面板随文档 tab 宽收放（先收目录、再收批注，目录默认收起，全局记忆）；悬停卡片出连线、移开消失、滚动跟随、**线色与划线一致**。
1. **P1**：`go build ./... && go test ./...` 全绿；`api-test-annotations.sh` 扩展覆盖回复 CRUD / 级联 / 越权 / 配额；客户端面板能展开已有回复（P0 的 UI 接真）。
2. **P2**：
   - 同一会话产出文档 → 批注 → 点「让 AI 回复」→ 回复正确利用会话上下文。
   - 换会话 / 很久后 → 回复靠文档 + 会话摘要 + 检索 + 工具，仍合理。
   - 归档会话的文档：回复仍能读到该会话摘要（不被 `is_deleted` 拦）。
   - 追问 → 线程追加，且新回复用到了此前讨论。
   - 生成失败：spinner 结束、错误可见、线程不出现半截状态。
3. **P3**：
   - 对 agent 说「看下批注逐条回复」→ 逐条落回复（`Source=chat`）。
   - 「问 AI」入口与引用胶囊/窄条全部消失，无残留死代码。
   - 注册表启动校验不 panic（`reply_annotation` 同步 `ReadOnlyFuncs`/`Catalog`）。
4. 改文档正文 → 批注重定位不受影响（回复独立表）；删批注 → 其回复一并删；文档物理清理（`purge`，回收站 7 天后 / 清空回收站）→ 批注与回复一起清。
5. 覆盖 `ctxflow` / prompt 的相关测试；`swiftc -typecheck` macOS / iOS 均 0 error。

---

## 十二、不做 / 待定

### 不做（v1 边界）

- ❌ 在批注表加 `parent_id` 自关联（AR-1）
- ❌ β：一条批注 = 一个可见/半可见子会话（AR-8）
- ❌ session window 级上下文定位（AR-6）
- ❌ 回复流式（v1 同步 + spinner）
- ❌ 按钮路径进当前会话（v1 为 document-scoped）
- ❌ 批注/回复进记忆 / 画像 / 摘要流水线（AR-11）
- ❌ 回复富文本 / 图片 / @ 引用
- ❌ trace 子会话（P1/P2 不做，留接口）
- ❌ 删除单条回复的 UI（`DELETE /api/annotation-replies/:id` v1 未做，后置）
- ❌ 连线的 Word 折线 / 多行环绕（v1 只画一条直线到首个行盒；见 AR-15）

### 待定

- ⏳ trace 子会话是否开启、粒度（一线程一子会话）
- ⏳ 原点会话近期轮 N 的取值
- ⏳ 按钮路径的**超时**与是否改异步 + 轮询（若 LLM 偶发很慢）
- ⏳ 单条回复上限 4000 是否够（AI 长回答）
- ⏳ 连线是否要在「展开线程（非悬停）」时保持（现仅悬停/指针在卡内时画）

---

## 附 · 施工备忘（self-contained，便于 compact 后复工）

### A. 关键事实与坐标

- 批注模型 `server/internal/model/document_annotation.go`：`Quote/Prefix/Suffix/StartOffset/EndOffset` + `Note/Orphaned/QuoteRewritten`。
- 批注 repo `server/internal/service/document_annotation_repo.go`：`List`（`:37`，按 `start_offset,id`）、`CountByDocument`（`:58`，配额 500）、`RelocateAll`（`:110`）。
- 批注 handler `server/internal/controller/annotation.go`：常量在 `:17-26`（`maxAnnotationQuoteLen=500`、`maxAnnotationNoteLen=2000`、`maxAnnotationsPerDoc=500`、`maxAnchorHintLen=60`）；`ListAnnotations` `:39`；路由 `controller/router.go:164-168`。
- 批注工具 `server/internal/tool/document.go`：`Functions()` `:196`、`ReadOnlyFuncs()` `:307`、`Catalog()` `:188`、`listAnnotations` `:596`、`NewDocumentTool` `:178`。
- MiniReAct `server/internal/agent/mini_react.go`：`MiniReActConfig` `:82`；`MsgSink` 可选 `:70`；`SubAgentExt` `:56`；种子历史不落库 `:36`。
- subagent 子会话建法 `server/internal/agent/subagent.go`：建 `subConv`（`ParentID`）`:508-518`；sink `subAgentMsgSink` `:341`；`IsSubAgent=true` `:351`。
- 会话列表隐藏子会话 `server/internal/service/conversation_repo.go:88`（`parent_id IS NULL`）。
- `Document.ConversationID *string` `server/internal/model/document.go:19`。
- 会话摘要 `ConversationSummary` 有 `CoverFromMsgID/CoverToMsgID`、`Status=active`。
- 归档 = 软删 `is_deleted=true`（`conversation_repo.go:176-180`），**消息不删**；仅**无痕**会物理销毁。
- 文档级联删批注/回复的实际位置 = `service/document_repo.go` 的 `purge`（物理清理事务；软删 `Delete` 不动批注/回复，留到 purge 一起删）。文档 API 删除 handler 里**没有**级联。
- `vector.message_retention_days: 14` 只影响语义检索（内存向量），**按 id 查库绕开**。
- `ExecuteContext`（`tool/tool.go:69`）**没有** message id，只有 `UserID/ConvID/TurnID/ToolCallID/Mode`。
- 问 AI 客户端残留点：`DocumentAIBridge.onAsk`、`ChatView.swift:161-170/383`、`ChatViewModel.swift:179-194`、`DocQuote.swift`。

### B. 回复落库规则

- 根 = `DocumentAnnotation.note`（不动）。
- 用户追问 = `DocumentAnnotationReply{role:"user"}`；AI 回复 = `{role:"assistant"}`。
- 按钮路径：run 的**最终 assistant 文本自动 append**（无需 agent 调工具）。
- 批量路径：agent 显式 `reply_annotation`。

### C. 上下文提示词要点（写进 `annotation_reply.go` 的 system prompt）

- 角色：帮用户回复他在文档某段划的线 / 写的批注。
- 输入：文档标题 + 批注 `quote`/`note` + 已有线程 + 原点会话摘要/近期轮。
- 明确 `quote` 是 **DOM 纯文本**（无 markdown 标记、空白已折叠），别拿去文档里精确搜。
- 需要全文/更多上下文时**用工具**（`document` 按窗口读、`unified_search`、`message_search`）。
- 输出长度约束（复用前序 `brief_answer` 的思路，但这里直接写进本 run 的 prompt，不改全局 system prompt 与缓存）。

### D. 命名 / 风格

- 中文注释与提交信息沿用项目风格。
- 错误：`fmt.Errorf("annotation reply: %w", err)` 逐层加语境，顶层记一次日志。
- 列表分页、批量 IN 防 N+1、活动埋点（`DocumentAnnotation` / 回复**不是** tracked 实体，无需埋点）。

### E. 客户端实现地图（P0 已落地，坐标供复工）

- 模型 `Shared/Models/DocumentAnnotation.swift`：`AnnotationReply`、`DocumentAnnotation.replyCount/lastReply`（可选）、`DocumentAnnotationListResponse`/`AnnotationReplyListResponse`/`AnnotationReplyCreateResponse`、`AnnotationNoteUpdate`。
- 接口 `Shared/Services/APIClient.swift`：`listAnnotationReplies` / `replyToAnnotation`（在 `relocateAnnotation` 之后）。
- 控制器 `Shared/Views/DocumentReaderView.swift`：`repliesByAnnotation` / `expandedAnnotationIDs` / `loadingReplyIDs` / `busyReplyIDs` / `connectorID` / `connectorRect`；`toggleThread` / `loadReplies` / `reply` / `forgetThread` / `beginConnectorTracking` / `endConnectorTracking` / `measureConnector`；`apply` 里 `case "scroll"`；`pushAnnotations` 末尾重测连线。
- JS（同文件的 `template` 字符串）：`annotationRect(id)`（`scrollToAnnotation` 之后）、`scroll` 监听补 `post({type:'scroll'})`、`return {…, annotationRect}` 导出。
- 视图 `Shared/Views/AnnotationDrawer.swift`：顶部 `AnnotationPanelMode` + `AnnotationHighlightColor` + `AnnotationConnectorLayer`（**跨平台**）；`#if os(macOS)` 内 `AnnotationPanel` / `AnnotationThreadCard` / `AnnotationListBody` / `AnnotationTime`；末尾 `#if DEBUG` 的 `DocPreviewShell` + `previewPanel(...)` + `WavyUnderline` + `ConnectorPreviewDemo` + 7 个 `#Preview`。
- 装配 `Shared/Views/DocumentListView.swift`（`DocumentFullView`）：`@AppStorage` 三键（`xhan_doc_annot_open`/`xhan_doc_annot_mode`/`xhan_doc_toc_collapsed`）、`docWidth`/`readerFrame`/`connectorID`/`connectorCardFrame`、`layoutWidth`/`tocExpanded`/`annotExpanded`/`connectorStart`/`connectorEnd`、`annotationPanel`/`annotationStrip`、HStack 上的 `onGeometryChange` + `.overlay { AnnotationConnectorLayer }`、`toggleAnnotationPanel()`。
- 常量 `Shared/Views/LayoutConstants.swift`：`docAnnotPanelWidth=300` / `docPanelStripWidth=32` / `docTocExpandedWidth=220` / `docReaderMinWidth=260` / `docAnnotHardFloor=560` / `docTocHardFloor=780`。
- 自查：`cd client/xHelper/xHelper/Shared && xcrun swiftc -typecheck -D DEBUG -sdk "$(xcrun --show-sdk-path --sdk macosx)" -target arm64-apple-macos26.3 -default-isolation MainActor -I <DerivedData>/Build/Products/Debug $(cat /tmp/shared.txt) ../../xHelperMac/*.swift`；iOS 换 `iphonesimulator` + `Index.noindex/Build/Products/Debug-iphonesimulator`。`bash client/xHelper/scripts/check-reader-html.sh` 现有 **179 条断言**（模板从 Swift 源现抠，故 JS 改动自动纳入；**未加连线专属断言**，可后续补）。
- 注意：`AnnotationPanelMode` / `AnnotationConnectorLayer` / `AnnotationHighlightColor` **必须在 `#if os(macOS)` 之外**定义（`@AppStorage` 默认值与 `.overlay` 在共享的 `DocumentListView` 里用到，iOS 也要能编）。

### F. 服务端实现地图（P1+P2+P3-Go 已落地）

- 模型 `model/document_annotation_reply.go`：`ID/AnnotationID/DocumentID/UserID/Role/Content/Source/CreatedAt`；索引 `annotation_id`/`document_id`/`user_id`。
- repo `service/document_annotation_reply_repo.go`：`CreateBatch`（单 INSERT 原子）/ `ListByAnnotationID`（`created_at ASC,id ASC`，limit<0→100，limit==0 只取总数）/ `CountByAnnotation` / `CountsByAnnotationIDs`（`GROUP BY`）/ `LatestByAnnotationIDs`（`ORDER BY annotation_id,created_at DESC,id DESC` 内存取首）/ `ListRecentByAnnotationIDs`（`ROW_NUMBER() OVER (PARTITION BY annotation_id ...)`，MySQL8/SQLite3.25+）/ `DeleteByAnnotation` / `DeleteByDocument`。
- 生成器 `agent/annotation_reply.go`：`NewAnnotationReplier`（必需依赖 nil→panic）；`Generate(ctx,userID,annotationID,userContent)`；哨兵错误 `ErrAnnotationReplyBusy/Limit/NotFound/Save`；常量 `annotationReplyMaxContentLen=4000`、`annotationReplyMaxReplies=100`、`annotationReplyTimeout=120s`、`annotationReplyMaxIterations=6`、`annotationReplyOriginRounds=10`、`annotationReplyThreadInPrompt=20`；只读工具白名单 `annotationReplyToolNames=[document,unified_search,message_search,get_summary]`；`loadOriginContext` 降级打日志；`buildUserMessage` 动态上下文全进 user 消息；`tryKeyedMutex.tryLock` 非阻塞。
- controller `controller/annotation.go`：`annotationItem`（嵌入 `model.DocumentAnnotation` + `reply_count`/`last_reply`）；`ListReplies`（limit≤200）；`ReplyAnnotation`（哨兵→409/404/400/500/502，`500/502` 顶层 `log.Printf` 一次）；`DeleteAnnotation` 级联删回复（失败仅记日志）。
- 工具 `tool/document.go`：`reply_annotation`（`source=chat`，校验归属/长度/上限）；`listAnnotations` 带 `replies`（≤20/批注、`replies_truncated`）；常量 `maxAnnotationReplyLen=4000`/`maxRepliesPerAnnotation=100`/`annotationRepliesPreview=20`。
- 装配：`appinit/tools.go` `NewDocumentTool(doc,anno,reply)`；`appinit/router.go` 建 `AnnotationReplier`（`Client: llmClients.Flash`）；`app/init_db.go` AllModels + `app/init_repos.go` Repos/InitRepos。
- 单测：`service/document_annotation_reply_repo_test.go`（排序/计数/最近N/删除）；`tool/document_test.go` 的 `TestDocumentAnnotationReplyAndList`。回归：`activity/track_test.go`、`service/document_repo_test.go` 的 AutoMigrate 已补 `DocumentAnnotationReply`（否则 purge 用例 no such table）。
