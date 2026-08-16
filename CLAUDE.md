# xhanHelper — 个人 AI Agent 助手

## 项目概述

以聊天为主要交互方式的个人 AI Agent，Go 单体后端 + Vue 3 前端 + Swift 客户端。

核心能力：用户通过自然语言对话，Agent 自主判断意图、调用工具（记账/笔记/文档/计算/智能体等）完成任务。支持向量记忆检索、动态工具注册、自定义智能体、System Prompt 版本管理和反思系统。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.22+, Gin, GORM, SQLite(dev)/MySQL(prod) |
| AI | DeepSeek / 豆包 API，LLM 抽象层可切换，DeepSeek 支持原生 function calling + 思考模式 |
| 前端 | Vue 3 + Naive UI + Pinia + Vue Router + Chart.js + Vite（`front-end/`） |
| 客户端 | Swift + SwiftUI（`client/`，macOS App） |
| 通信 | SSE (流式对话) + HTTP REST |
| 向量 | 豆包 Embedding API + 内存向量存储（余弦相似度检索） |

## 目录结构

```
xhanHelper/
├── server/                             # Go 后端
│   ├── cmd/xhan-helper/main.go         # 入口：初始化 DB/LLM/Tools/Agents/Router
│   ├── conf/                           # config.yaml + config.dev/prod/test.yaml
│   ├── internal/
│   │   ├── config/                     # YAML 加载 + 环境合并 + 验证
│   │   ├── model/                      # GORM 模型 (22 个)
│   │   ├── service/                    # DB Repo 层 (24 个文件)
│   │   ├── controller/                 # Gin handlers (20 个文件)
│   │   ├── middleware/                 # 用户认证 + 请求日志
│   │   ├── llm/                        # LLM 抽象层 (DeepSeek/豆包/Claude + Embedding)
│   │   ├── agent/                      # 核心 Agent 系统
│   │   │   ├── orchestrator.go         # ReAct 编排器
│   │   │   ├── pipeline/               # 消息预处理管线（日期规范化/自动路由）
│   │   │   ├── promptbuilder/          # System Prompt 三层构建器
│   │   │   ├── retrieval/              # 向量检索（记忆/笔记/历史/主题）
│   │   │   └── replay/                 # 对话重放对比工具
│   │   ├── tool/                       # 工具接口 + 注册表 + 15+ 内置工具
│   │   ├── view/                       # SSE/JSON 渲染
│   │   ├── app/                        # 基础设施抽象（LLM/Tool/Repo 接口）
│   │   ├── appinit/                    # 应用初始化编排
│   │   ├── idgen/                      # Snowflake ID 生成
│   │   ├── mcp/                        # MCP 协议支持
│   │   ├── monitor/                    # 监控看板 + Admin 中间件
│   │   └── util/                       # 工具函数
│   ├── scripts/api-test.sh             # API 自动化测试
│   ├── migrations/                     # SQL 迁移脚本
│   └── Makefile                        # dev/test/build/clean
├── front-end/                          # Vue 3 前端
│   ├── src/
│   │   ├── views/                      # 页面 (Chat/Admin/Agent/Bills/Notes/Memory/Mailbox/...)
│   │   ├── components/                 # 组件 (MessageBubble/ToolCallToast/PlanGraph/...)
│   │   ├── composables/                # 组合式 API (useChatSSE/useDebugMode/...)
│   │   ├── stores/                     # Pinia (auth/chat/conversations/theme)
│   │   └── api/                        # HTTP 客户端封装
│   └── dist/                           # 构建产物
├── client/                             # Swift macOS 客户端
│   └── xHelper/
│       ├── xHelper/                    # iOS App
│       └── xHelperMac/                 # macOS App
└── docs/                               # 技术文档
    ├── design/                         # 设计文档 (phase-1 ~ phase-12)
    ├── agents/                         # Agent 技能文档
    ├── bugs/                           # Bug 记录
    ├── code-review/                    # 代码审查
    ├── front-end/                      # 前端设计
    └── research/                       # 调研
```

## 架构

```
HTTP Request
  → middleware/logging.go + auth.go      请求日志, Bearer Token 认证, 注入 user_id
  → controller/chat.go                   解析参数
     → pipeline/                         预处理管线（短输入检测/日期规范化/自动路由）
     → agent/orchestrator.go             ReAct 循环 (最多 N 轮)
        ├── promptbuilder.Build()        三层 System Prompt 构建
        │   ├── Layer 1: 公共规则 + 工具定义 + 当前时间
        │   ├── Layer 2: 角色定义（Agent > 用户自定义 > 默认）
        │   └── Layer 3: 检索上下文（主题/记忆/笔记/历史，按综合分排序）
        ├── llmClient.Chat()             流式调用 LLM（支持 thinking 模式）
        ├── parseLLMResponse()           解析 Flash 文本中的 JSON tool_calls
        ├── executeConcurrently()        并发执行工具
        │   └── registry.ExecuteByName()
        └── saveAssistantMsg/ToolMsg     消息持久化
  → service/*_repo.go                    DB CRUD
  → view/render.go                       SSE 流式输出
```

## 数据模型

### User + UserToken — 用户认证
用户注册/登录获取 Bearer token。

### Conversation — 会话
关联 user_id/agent_id，支持重命名/软删除，Extra 字段存 used_tool_ids 和主题上下文。

### Message — 消息
role: user/assistant/tool，可存储 reasoning_content、tool_calls_json、request_json/response_json（调试）、Extra（PromptTrace 追溯）、Embedding 向量。

### Bill — 账单
分类枚举（category1/2、consume_type、pay_type），支持 CSV 批量导入导出。

### Agent — 自定义智能体
用户可创建多个智能体，每个有独立的 system_prompt 和模型配置（ParamsJSON）。

### ToolRegistry — 动态工具
运行时从 DB 加载工具定义（schema_json），HTTP 转发到 Python sidecar 执行。

### Note — 笔记
带向量嵌入，支持语义检索。

### UserMemory — 长期记忆
向量存储 + 自动提取（MemoryExtractor 后台调度）。

### UserPrompt — 用户自定义 System Prompt
版本化管理，支持回滚。

### Document — 文档
Markdown 文档，带版本快照，支持标签检索。

### ThemeState — 主题状态
追踪用户身上正在发生的事，支持状态流转和生命周期管理。

### MailboxItem — 信箱
反思系统产生的通知/建议/发现，支持已读/归档/发起对话。

### 其他模型
Embedding、EmbeddingChunk、ConceptEmbedding、ReflectionTrace、ReflectionLLMCall、ReflectionDecision、PendingFinding、Commitment、DelayedTask、ContentVersion、MCPServerRecord、ScheduledTask

## LLM 调用方式

**LLMClient 接口** (`llm/adapter.go`)：`Chat`（流式）、`ChatSync`（同步）、`BuildSystemPrompt`、`ModelName`。

**双模型架构**：
- Flash 模型（deepseek-v4-flash，日常对话，快速响应）
- Pro 模型（deepseek-v4-pro，思考模式，深度推理，可选）

**两种工具调用路径**：
1. **原生 function calling**（DeepSeek Pro）：LLM 直接返回 tool_calls，通过 `ToolCallID` 关联结果
2. **Prompt engineering**（Flash 文本解析）：System prompt 注入工具 JSON 定义 → LLM 返回 JSON `{"reply":"...","tool_calls":[...]}` → `parseLLMResponse()` 解析

**Provider 支持**：
- `deepseek`：Flash + Pro，主力
- `doubao`：仅 Flash（prompt engineering 方式）
- `claude`：Stub，未实现

**流式事件类型**：`text_delta` | `thought_delta` | `thought_done` | `tool_call` | `tool_result` | `plan_node_start` | `plan_node_done` | `plan` | `status` | `agent_matched` | `done` | `error`

## Tool 系统

任何工具实现 `tool.Tool` 接口即可注册：

```go
type Tool interface {
    Name() string
    Description() string
    Functions() []FuncSchema
    Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result
}
```

**内置工具**：

| 工具 | 主要函数 | 说明 |
|------|----------|------|
| bookkeeping | create/update/delete/query/stats/export/import | 记账 |
| datetime | now/today/weekday/days_ago/format | 日期时间 |
| calculator | calculate | 表达式求值 |
| note | record/search/update/delete/list | 笔记（带向量检索） |
| agent | create/update/delete/list | 智能体管理 |
| document | create/get/update/delete/query/rollback/versions | 文档管理（版本快照） |
| memory_search | search | 语义搜索长期记忆 |
| message_search | search | 语义搜索历史消息 |
| tool_retriever | search | 工具发现（按意图匹配工具） |
| mailbox | list/mark_read/archive | 信箱管理 |
| get_topic_detail | get | 主题详情查询 |
| get_topic_stats | stats | 主题统计（活跃/休眠/总计） |
| get_bill_stats | stats | 账单统计（按分类/时间聚合） |

**动态工具**：`Registry` 每 30s 从 DB 加载活跃的动态工具，MCP 协议转发执行。添加新工具：DB 插入 `tool_registries` 记录即可，无需重启。

## 记忆系统

用户消息自动向量化 → 后台 `MemoryScheduler` 定期扫描活跃会话 → `MemoryExtractor` 调用 LLM 提取结构化记忆 → 去重（余弦相似度 > 0.85）→ 写入 `UserMemory` + 向量存储。

对话时 `Retriever` 检索相关记忆/笔记/历史对话注入 system prompt，提升回复质量。

## 反思系统

周期性分析（每日多次调度）→ `ReflectionOrchestrator` 调用 LLM 反思 → 产出发现/建议/提醒 → 投递到 `Mailbox` → 用户可归档或发起对话跟进。

## 配置 (`conf/config.yaml`)

```yaml
server:
  port: 8088
  mode: debug
  registration_enabled: true
  allowed_users: []
  memory_auto_extract: false
llm:
  provider: deepseek
  api_key: ${LLM_API_KEY}
  model: deepseek-v4-flash
  strong_model: deepseek-v4-pro
  base_url: https://api.deepseek.com
  max_iterations: 8
  max_tool_result_chars: 2000
embedding:
  api_key: ${LLM_API_KEY}
  base_url: https://ark.cn-beijing.volces.com
  model: doubao-embedding-vision-251215
reflection:
  daily_schedules: [...]
  launch_max_iterations: 5
retrieval:
  memory: { threshold: 0.5, limit: 8, vector_weight: 0.5, ... }
  note: { threshold: 0.5, limit: 5, ... }
  history: { threshold: 0.8, limit: 5, ... }
```

- `APP_ENV=dev` 使用 SQLite，`APP_ENV=prod` 使用 MySQL，`APP_ENV=test` 用 SQLite 跑测试
- 敏感信息用 `${VAR}` 占位符，环境变量注入

## 常用命令

```bash
cd server
make dev          # 启动开发服务器
make test         # API 自动化测试（编译→启动→测试→停止）
make build        # 编译生产二进制
make clean        # 清理
```

## API 接口

### 认证
| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/api/auth/register` | 无 | 用户注册 |
| POST | `/api/auth/login` | 无 | 用户登录 |
| POST | `/api/auth/logout` | Bearer | 登出 |

### 对话
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/chat` | SSE 流式对话 |
| GET | `/api/conversations` | 会话列表 |
| GET | `/api/conversations/:id/messages` | 消息历史（offset 分页） |
| GET | `/api/conversations/:id/messages-v2` | 消息历史（cursor 分页） |
| DELETE | `/api/conversations/:id/messages/:msgId` | 删除消息 |
| PATCH | `/api/conversations/:id` | 重命名会话 |
| DELETE | `/api/conversations/:id` | 删除会话 |

### 账单 / 笔记 / 记忆 / 文档
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/bills` | 账单列表 |
| GET/POST | `/api/bills/export` `/api/bills/import` | CSV 导出/导入 |
| GET/POST/DELETE | `/api/notes` | 笔记 CRUD |
| GET/DELETE/POST | `/api/memories` | 记忆管理 |
| GET/POST/DELETE | `/api/documents` | 文档管理 |

### 智能体 / 工具 / Prompt
| 方法 | 路径 | 说明 |
|------|------|------|
| CRUD | `/api/agents` | 智能体管理 |
| GET/POST/PATCH/DELETE | `/api/tools` | 动态工具管理 |
| GET/POST/DELETE | `/api/prompts` | System Prompt 版本管理 |

### 信箱 / 主题 / 反思
| 方法 | 路径 | 说明 |
|------|------|------|
| GET/PATCH/POST | `/api/mailbox` | 信箱管理 |
| GET/POST/PATCH | `/api/themes` | 主题管理 |
| POST | `/api/reflection/trigger` | 手动触发反思 |

### 管理端 (Admin)
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/admin/health` | 健康检查 |
| GET | `/api/admin/messages/:id/debug` | 消息调试（一轮完整消息） |
| GET | `/api/admin/messages/:id/debug-detail` | 消息重量级字段 |
| GET/POST | `/api/admin/resource-lookup` | 资源查询（记忆/笔记/历史） |
| POST | `/api/admin/replay/preview` | 重放预览（消息+system prompt） |
| POST | `/api/admin/replay/compare` | 对话重放对比（SSE） |
| GET | `/api/admin/message-traces` | Prompt Trace 列表 |
| GET | `/api/admin/reflection/traces` | 反思追溯 |

## 开发约定

- 新增功能先在 `docs/design/` 写计划文档，确认后开发
- 编码完成后必须 `make test` 全绿
- **改动的验收分级（按影响面选）**：
  - 影响主链路（聊天/工具调用/SSE/数据库写入）：完整 `make test` + 新增针对性单测
  - 只影响后台低危任务（记忆/摘要/主题/反思等定时处理）：轻量验收即可——`go build ./...` + `go test ./...` 全绿 + `vite build`（如改前端）+ 真机跑一次聊天并检查日志无 `[post_chat_group]` 等报错。不必跑 `make test`（该脚本因测试数据库残留有既有失败，与改动无关）
- model/ 只放结构体，DB 操作放 service/
- 工具实现放 tool/，不要耦合到 controller 或 agent
- 从数据库列表拉取数据时，必须做分页
- **禁止在循环内逐个访问数据库**（每轮迭代一次 DB 调用 = N+1 查询）：批量操作替代——插入用 `gorm` 切片批量 Create（`tx.Create(&slice)`），更新/删除用带 `IN` 条件的 `Updates`/`Delete`；循环里只做内存操作
- 修改代码前必须先说明方案，征得用户同意
- 提交代码前必须征得用户同意
- 遇到相似功能，抽取公共组件
- **客户端（client/）代码不由 agent 编译验证**：agent 只改代码，不执行 xcodebuild，编译和运行验证由用户在 Xcode 中完成（避免与 Xcode 并发构建导致 build database 锁冲突）
- **禁止对必要依赖做 nil 检查后静默返回**：所有通过依赖注入传入的必需组件（repo、client、worker 等），如果为 nil 意味着启动阶段的接线 bug。不要写 `if x == nil { return }` 让它无声失败——让它 panic，炸在明处。可选依赖（如回调函数）可以用 nil 检查

## 后端错误处理规范

- **所有 error 必须打日志**，严禁 500 或其他错误状态下不记录原因
- **一个 error 只打一次日志**，禁止同一 error 在多层重复打印
- 不打断流程的 error（可降级继续）：就地 `log.Printf` 后继续执行
- 需向上抛出的 error：通过 `fmt.Errorf("context: %w", err)` 附加信息后返回，在调用链最顶层 `log.Printf` 一次
- 所有 `view.JSONError(c, 500, ...)` 前必须有 `log.Printf` 记录原始 error
