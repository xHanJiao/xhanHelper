# LLM 调用审计 — 设计文档

> 版本：v1 | 日期：2026-08-16 | 状态：设计完成，待开发

## 1. 背景与问题

后处理系统（记忆提取/画像提取/摘要等后台 worker）的 LLM 调用是"黑盒"：输入（system prompt + user message XML）与输出（LLM 返回 JSON）零持久化，只能靠日志碎片调试。

近期画像 fact 化重构（profile-fact-structure.md）让问题更突出——画像提取质量（LLM 看到的 existing 上下文、输出的 facts 列表、配额重生成轮次）直接影响聊天质量，需要可见、可回溯的审计能力。

## 2. 现状调研

| 机制 | 覆盖范围 | 内容 | 缺口 |
|------|---------|------|------|
| `LlmCostDetail`（llmcost CostTracker 装饰器） | 所有 Chat/ChatSync | 仅 token 用量 + category/conv/ref | 无 I/O 内容 |
| `ReflectionLLMCall` | 仅反思 | request/response JSON ✓ | 覆盖窄 |
| `messages.request_json`（DebugPersistJSON） | 仅主聊天 ReAct | 请求 JSON，异步限频 | 不覆盖后处理 |
| monitor 快照 | 全局 | 内存计数/延迟 | 无内容 |

**结论**：后处理 worker 的 LLM I/O 是唯一零审计的路径。反思已有追溯（保留现状），主聊天已有 debug_json（保留现状），本次补齐后处理 + 两个低频场景。

## 3. 目标与范围

**目标**：所有后处理 LLM 调用可审计——监控页面可见每次调用的输入输出，可回溯排查（"上周那轮画像提取为什么拆错了"）。

**范围**（category + sub_category 白名单）：

| 类别 | 子场景 | 说明 |
|------|--------|------|
| post_chat | memory_extract | 记忆提取（MemoryExtractor） |
| | profile_extract | 画像增量提取（含延迟迁移） |
| | profile_cold_start | 画像冷启动 ReAct（多轮） |
| | summary | 会话摘要 |
| admin | agent_summary | 智能体摘要生成 |
| tool | reminder_gen | 提醒事项生成 |
| （兜底） | uncategorized | 未打 Scenario 标注的调用 |

**明确不覆盖**：
- reflection（10 子类）——已有 ReflectionLLMCall 追溯 + 前端反思 tab，避免双写
- main_chat（主聊天 ReAct）——已有 request_json 机制，且流式内容巨大
- 流式 Chat 路径——见 4.1

## 4. 方案设计

### 4.1 拦截点：ChatSync 装饰器（不处理流式）

**验证结论**：目标场景**全部走 ChatSync**：

| 场景 | 调用方式 | 位置 |
|------|---------|------|
| profile_extract / profile_cold_start | ChatSync | `profile_worker.go`、`mini_react.go` |
| memory_extract | ChatSync | `extractor.go:125` |
| summary | ChatSync | `summary_worker.go:140` |
| agent_summary | ChatSync | `controller/agent.go` |
| reminder_gen | ChatSync | `tool/reminder.go:340` |

全库唯一流式调用是主聊天 ReAct（`orchestrator.go:839`）——不在审计范围。**装饰器只实现 ChatSync 捕获，流式 Chat 零实现**。

**实现位置**：扩 `llmcost` 装饰器（`CostTracker`）——包装链 `CostTracker → MonitoredLLMClient → 真实客户端` 是全局唯一 LLM 出口，一处插桩覆盖所有调用方，各 worker 零改动。捕获逻辑：

```
ChatSync(ctx, req):
  start := now
  msg, err := inner.ChatSync(ctx, req)
  scenario := ScenarioFromContext(ctx)
  if shouldAudit(scenario.Category, scenario.SubCategory):
      go recordIO(...)  // 异步，不阻塞
  return msg, err
```

**失败也记**：err != nil 时记录请求 + ErrorMsg（审计价值在失败排查），无响应字段。

### 4.2 数据模型

```sql
CREATE TABLE llm_call_io (
  id                VARCHAR(36) NOT NULL PRIMARY KEY,
  category          VARCHAR(30) NOT NULL,
  sub_category      VARCHAR(50) NOT NULL,
  user_id           VARCHAR(36) NOT NULL DEFAULT '',
  conv_id           VARCHAR(36) NOT NULL DEFAULT '',
  ref_type          VARCHAR(30) NOT NULL DEFAULT '',
  ref_id            VARCHAR(36) NOT NULL DEFAULT '',
  model             VARCHAR(50) NOT NULL,
  system_prompt     TEXT,
  messages_json     TEXT,
  response          TEXT,
  prompt_tokens     BIGINT NOT NULL DEFAULT 0,
  completion_tokens BIGINT NOT NULL DEFAULT 0,
  latency_ms        BIGINT NOT NULL DEFAULT 0,
  error_msg         TEXT,
  created_at        DATETIME NOT NULL,
  INDEX idx_io_created (created_at),
  INDEX idx_io_category (category, sub_category),
  INDEX idx_io_conv (conv_id)
);
```

```go
type LLMCallIO struct {
    ID               string    `gorm:"primaryKey;size:36"`
    Category         string    `gorm:"type:varchar(30);not null"`
    SubCategory      string    `gorm:"type:varchar(50);not null"`
    UserID           string    `gorm:"size:36;not null;default:''"`
    ConvID           string    `gorm:"size:36;not null;default:'';index:idx_io_conv"`
    RefType          string    `gorm:"type:varchar(30);not null;default:''"`
    RefID            string    `gorm:"size:36;not null;default:''"`
    Model            string    `gorm:"type:varchar(50);not null"`
    SystemPrompt     string    `gorm:"type:text"`
    MessagesJSON     string    `gorm:"type:text"`
    Response         string    `gorm:"type:text"`
    PromptTokens     int64     `gorm:"not null;default:0"`
    CompletionTokens int64     `gorm:"not null;default:0"`
    LatencyMs        int64     `gorm:"not null;default:0"`
    ErrorMsg         string    `gorm:"type:text"`
    CreatedAt        time.Time `gorm:"autoCreateTime;index:idx_io_created"`
}
```

- `MessagesJSON`：`[]llm.Message` 序列化（role/content/tool_calls）
- `Response`：`msg.Content`（ChatSync 全量；reasoning 不存——审计重点是 I/O 语义，thought 大且价值低，可后续加）
- 大字段（system_prompt/messages_json/response/error_msg）**不进列表查询**，详情单独取

### 4.3 过滤规则（白名单）

```go
var auditWhitelist = map[string]map[string]bool{
    llmcost.CategoryPostChat: {
        llmcost.SubMemoryExtract:    true,
        llmcost.SubProfileExtract:   true,
        llmcost.SubProfileColdStart: true,
        llmcost.SubSummary:          true,
    },
    llmcost.CategoryAdmin: {llmcost.SubAgentSummary: true},
    llmcost.CategoryTool:  {llmcost.SubReminderGen: true},
}
// category 为空（未标注）→ uncategorized 兜底记录
```

白名单显式声明，未来扩场景改一处。

### 4.4 写入与失败处理

- **异步**：`go recordIO(...)`，与 llmcost token 记录同模式，不阻塞 LLM 响应链路
- **失败打日志**：`log.Printf("[llm_audit] insert error: %v")` 一次，不重试（审计是旁路，失败不炸主流程）
- **频次估算**：session_end 每次对话 4 worker × 1-2 次 + 冷启动多轮 + 低频 admin/tool → 每天几十条，全量记录无压力

## 5. API 设计（Admin 中间件）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/admin/llm-io?category=&sub_category=&conv_id=&user_id=&limit=&offset=` | 分页列表（**不含大字段**，返回 id/category/sub/model/conv/tokens/latency/error 摘要/时间） |
| GET | `/api/admin/llm-io/:id` | 详情（含 system_prompt/messages_json/response/error_msg） |

- 默认按 created_at 倒序；limit 默认 50 上限 200
- 错误调用（error_msg 非空）列表行加标记

## 6. 清理策略

保留 7 天。参照 `debug_json` 清理模式：启动时 + 每日定时 `CleanupBefore(7 天前)`，按 created_at 索引批量删除（单批 limit 控制，防长事务）。

## 7. 前端设计

AdminView 新增 tab **"LLM 审计"**。

### 7.1 页面结构

```
┌────────────────────────────────────────────────────────────────────┐
│  [概览] [反思] [消息Trace] [主题发现] [Token 消耗] [LLM 审计]      │
└────────────────────────────────────────────────────────────────────┘
```

列表页：

```
┌──────────────────────────────────────────────────────────────────────────┐
│ LLM 审计                                                                  │
│                                                                          │
│  [类别 ▼ 全部] [子类 ▼ 全部] [会话ID______] [刷新]   共 87 条 · 保留 7 天 │
│                                                                          │
│ ┌────┬──────────────┬──────────────┬────────────┬──────┬──────┬────────┐ │
│ │ 类别│ 时间          │ 子类          │ 模型        │ Tokens│ 耗时 │ 状态   │ │
│ ├────┼──────────────┼──────────────┼────────────┼──────┼──────┼────────┤ │
│ │🟦  │ 08-16 14:32   │ profile_extract│ deepseek-… │ 3.2k │ 1.8s │  ✓     │ │
│ │🟦  │ 08-16 14:32   │ summary       │ deepseek-… │ 1.1k │ 0.9s │  ✓     │ │
│ │🟦  │ 08-16 14:32   │ memory_extract│ deepseek-… │ 2.4k │ 2.2s │  ✓     │ │
│ │🟨  │ 08-16 13:05   │ reminder_gen  │ deepseek-… │ 0.8k │ 0.6s │  ✓     │ │
│ │🟪  │ 08-16 11:40   │ agent_summary │ deepseek-… │ 1.5k │ 1.1s │  ✓     │ │
│ │🟦  │ 08-16 10:02   │ profile_cold_ │ deepseek-… │ 8.6k │ 21.3s│  ✓     │ │
│ │    │              │  start (6轮)   │            │      │      │        │ │
│ │⬜  │ 08-15 22:18   │ uncategorized │ deepseek-… │ 0.9k │ 0.4s │  ✓     │ │
│ │🟦  │ 08-15 21:47   │ profile_extract│ deepseek-… │ 2.9k │ 1.5s │ ✗ 失败 │ │
│ └────┴──────────────┴──────────────┴────────────┴──────┴──────┴────────┘ │
│                                                   [< 1 2 3 … 9 >]        │
└──────────────────────────────────────────────────────────────────────────┘
```

详情抽屉（右侧滑出）：

```
┌──────────────────────────────┬───────────────────────────────────────────┐
│ 列表（变暗）                   │ ✕ LLM 调用详情                           │
│                              │  profile_extract · deepseek-v4-flash      │
│                              │  08-16 14:32 · conv_9f3x · 3.2k tok ·1.8s │
│                              ├───────────────────────────────────────────┤
│                              │ ▼ System Prompt          [复制]           │
│                              │ ┌───────────────────────────────────────┐ │
│                              │ │ 你是一个对话后处理助手。负责对用户对话 │ │
│                              │ │ 记录进行批量处理与分析。…             │ │
│                              │ └───────────────────────────────────────┘ │
│                              │ ▼ Messages (user)          [复制]         │
│                              │ ┌───────────────────────────────────────┐ │
│                              │ │ {                                     │ │
│                              │ │   "role": "user",                     │ │
│                              │ │   "content": "<profile_extraction>…" │ │
│                              │ │ }                                     │ │
│                              │ └───────────────────────────────────────┘ │
│                              │ ▼ Response                 [复制] [格式化]│
│                              │ ┌───────────────────────────────────────┐ │
│                              │ │ {"职业领域":["5年Go后端"],            │ │
│                              │ │  "称呼":["哥"]}                       │ │
│                              │ └───────────────────────────────────────┘ │
│                              │ ✗ 失败行额外显示 ▼ Error                  │
│                              │   ┌───────────────────────────────────┐  │
│                              │   │ context deadline exceeded         │  │
│                              │   └───────────────────────────────────┘  │
└──────────────────────────────┴───────────────────────────────────────────┘
```

### 7.2 要点

- 类别徽标色：🟦 post_chat / 🟪 admin / 🟨 tool / ⬜ uncategorized
- 冷启动显示轮数（从 messages 长度推断 tool 轮次，列表副标题）
- 详情三栏默认折叠（`▼`），内容量大时先看结构再展开
- 失败行红色 ✗，详情优先展示 Error
- 列表行不携带大字段（加载快），点开才拉 `/:id` 详情
- 复用 MessageDebugView 的展示风格（等宽字体 + 折叠 + 复制按钮）

## 8. 文件清单

| 类型 | 文件 | 改动 |
|------|------|------|
| 新建 | `internal/model/llm_call_io.go` | LLMCallIO 模型 |
| 新建 | `migrations/025_create_llm_call_io.sql` | 建表 + 索引 |
| 改动 | `internal/llmcost/decorator.go` | ChatSync 捕获 + 白名单过滤 + 异步记录 |
| 改动 | `internal/llmcost/recorder.go` / `db_recorder.go` | Recorder 接口扩 RecordIO + DB 实现 |
| 新建 | `internal/service/llm_call_io_repo.go` | Create/List（筛选分页，无大字段）/GetByID/CleanupBefore |
| 新建 | `internal/controller/llm_io_admin.go` | 列表 + 详情 handler |
| 改动 | `internal/appinit/` | repo 注入、路由注册、清理任务接入 |
| 改动 | 前端 `AdminView.vue` + api 封装 + 新组件 | LLM 审计 tab |
| 新建 | 测试 | 装饰器过滤（post_chat 记/reflection 跳/uncategorized 记/失败记）、repo、API |

## 9. 测试计划

1. **装饰器单测**（syncMockLLM）：post_chat 记、reflection 跳、main_chat 跳、uncategorized 记、err 记（含 ErrorMsg）、Messages/SystemPrompt/Response 内容正确、LatencyMs > 0
2. **repo 单测**：分页筛选（category/sub/conv 组合）、GetByID、CleanupBefore 只删旧数据
3. **API 单测**：列表不含大字段、详情含全部字段、limit 上限
4. **验证**：`go build ./...` + `go test ./...` + `vite build` + 真机跑一次对话，监控页面可见 profile_extract/summary/memory_extract 记录且 I/O 内容完整

## 10. 决策记录

| 日期 | 决策 |
|------|------|
| 2026-08-16 | 审计方案选 DB 表（可回溯），不做内存 ring buffer |
| 2026-08-16 | 范围：post_chat 全 4 子类 + admin/agent_summary + tool/reminder_gen；reflection 不进入（已有追溯，避免双写）；main_chat 不进入（已有 debug_json） |
| 2026-08-16 | 拦截点只做 ChatSync（目标场景验证全部 ChatSync，唯一流式是主聊天不审计） |
| 2026-08-16 | 全量记录（每天几十条量级），保留 7 天清理 |
| 2026-08-16 | 失败调用也记录（请求 + ErrorMsg） |
| 2026-08-16 | 未标注 scenario 的调用记 uncategorized 兜底 |
| 2026-08-16 | 前端 AdminView 新 tab "LLM 审计"，列表不含大字段、详情单独取 |
