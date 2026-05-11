# xhanHelper — 个人 AI Agent 助手

## 项目概述

以聊天为主要交互方式的个人 AI Agent，Go 单体后端 + Vue 3 Web 前端（开发中）。

核心能力：用户通过自然语言对话，Agent 自主判断意图、调用工具（记账/笔记/计算/智能体等）完成任务。支持向量记忆检索、动态工具注册、自定义智能体和 System Prompt。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.22+, Gin, GORM, SQLite(dev)/MySQL(prod) |
| AI | DeepSeek / 豆包 API，LLM 抽象层可切换，DeepSeek 支持原生 function calling + 思考模式 |
| 前端 | Vue 3 + Naive UI + Vite（`web/`，计划阶段，见 `docs/front-end/plan.md`） |
| 通信 | SSE (流式对话) + HTTP REST |
| 向量 | 豆包 Embedding API + 内存向量存储（余弦相似度检索） |

## 目录结构

```
xhanHelper/
├── server/                        # Go 后端
│   ├── cmd/xhan-helper/main.go    # 入口：初始化 DB/LLM/Tools/Orchestrator
│   ├── conf/                      # config.yaml + config.dev/prod.yaml
│   ├── internal/
│   │   ├── config/                # YAML 加载 + 环境合并 + 验证
│   │   ├── model/                 # GORM 模型 (11 个)
│   │   ├── service/               # DB Repo 层 (10 个)
│   │   ├── controller/            # Gin handlers (11 个)
│   │   ├── middleware/            # 用户认证 + 请求日志
│   │   ├── llm/                   # LLM 抽象层 (接口 + 工厂 + DeepSeek/豆包/Claude 实现 + Embedding)
│   │   ├── agent/                 # ReAct 编排器 + 记忆系统 (检索/提取/向量存储/调度)
│   │   └── tool/                  # 工具接口 + 注册表 + 7 个内置工具 + 动态工具
│   ├── scripts/api-test.sh        # API 自动化测试
│   └── Makefile                   # dev/test/build/clean
├── web/                           # Vue 3 前端（计划阶段）
├── docs/                          # 技术文档 (后端参考/代码审查/bug 记录/设计阶段)
└── design/                        # 早期产品文档
```

## 架构

```
HTTP Request
  → middleware/logging.go + auth.go     请求日志, Bearer Token 认证, 注入 user_id
  → controller/chat.go                  解析参数, 调用 Orchestrator
  → agent/orchestrator.go               ReAct 循环 (最多 N 轮)
     ├── retriever.Retrieve()           向量检索相关记忆/笔记/历史对话
     ├── llmClient.Chat()               流式调用 LLM（支持 thinking 模式）
     ├── 原生 tool_calls → convertToolCalls() → executeConcurrently()
     │   └── registry.ExecuteByName()   并发执行工具
     ├── Flash 文本解析 → parseLLMResponse() → 执行嵌入的工具调用
     └── saveAssistantMsg/ToolMsg       消息持久化 + 向量化
  → service/*_repo.go                   DB CRUD
  → view/render.go                      SSE 流式输出
```

## 数据模型

### User + UserToken — 用户认证
用户注册/登录获取 Bearer token，替代早期的 device-centric 认证。

### Conversation — 会话
关联 user_id，支持重命名/删除。

### Message — 消息
支持 role: user/assistant/tool，可存储 reasoning_content、tool_calls_json、LLM 请求/响应 JSON（调试用）、Embedding 向量。

### Bill — 账单
带分类枚举（category1/2、consume_type、pay_type），支持 CSV 批量导入导出。

### Agent — 自定义智能体
用户可以创建多个智能体，每个有独立的 system_prompt 和模型配置。

### ToolRegistry — 动态工具
运行时可从 DB 加载工具定义（schema_json），通过 HTTP 转发到 Python sidecar 执行。

### ToolData — 工具数据缓存
工具执行结果缓存，支持过期时间。

### Note — 笔记
带向量嵌入，支持语义检索。

### UserMemory — 长期记忆
向量存储 + 自动提取（MemoryExtractor 后台调度）。

### UserPrompt — 用户自定义 System Prompt
版本化管理，支持回滚。

### ScheduledTask — 定时任务
模型已定义，功能未启用。

## LLM 调用方式

**LLMClient 接口** (`llm/adapter.go`)：`Chat`（流式）、`ChatSync`（同步）、`BuildSystemPrompt`、`ModelName`。

**双模型架构**：
- Flash 模型（日常对话，快速响应）
- Pro 模型（DeepSeek 思考模式，深度推理，可选）

**两种工具调用路径**：
1. **原生 function calling**（DeepSeek）：LLM 直接返回 tool_calls，通过 `ToolCallID` 关联结果
2. **Prompt engineering**（豆包/Flash 文本解析）：System prompt 注入工具定义 → LLM 返回 JSON `{"reply":"...","tool_calls":[...]}` → `parseLLMResponse()` 解析

**Provider 支持**：
- `deepseek`：Flash + Pro（原生 function calling + 思考模式），主力
- `doubao`：仅 Flash（prompt engineering 方式）
- `claude`：Stub，未实现

**流式事件类型**：`text_delta` | `thought_delta` | `thought_done` | `tool_call` | `tool_result` | `plan_node_start` | `plan_node_done` | `plan` | `status` | `done` | `error`

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

| 工具 | 函数 | 说明 |
|------|------|------|
| bookkeeping | create/update/delete/query/stats/export/import | 记账 |
| datetime | now/today/weekday/days_ago/format | 日期时间 |
| uuid | generate/batch | UUID 生成 |
| calculator | calculate | 表达式求值 |
| note | create/search/update/delete/list | 笔记（带向量检索） |
| agent | create/update/delete/list | 智能体管理 |

**动态工具**：`Registry` 每 30s 从 DB 加载活跃的动态工具，通过 HTTP 转发到 Python sidecar 执行。添加新工具：DB 插入 `tool_registries` 记录即可，无需重启服务。

## 记忆系统

用户消息自动向量化 → 后台 `MemoryScheduler` 定期扫描活跃会话 → `MemoryExtractor` 调用 LLM 提取结构化记忆 → 去重（余弦相似度 > 0.85）→ 写入 `UserMemory` + 向量存储。

对话时 `Retriever` 检索相关记忆/笔记/历史对话注入 system prompt，提升回复质量。

## 配置 (`conf/config.yaml`)

```yaml
server:
  port: 8088
  mode: debug
  registration_enabled: true       # 是否允许新用户注册
  allowed_users: []                # 允许注册的用户名（空=全部允许）
  memory_auto_extract: false       # 是否自动提取记忆
  sidecar_url: ""                  # Python sidecar MCP HTTP 地址
llm:
  provider: deepseek               # doubao | deepseek
  api_key: ${LLM_API_KEY}
  model: deepseek-chat             # Flash 模型
  strong_model: deepseek-reasoner  # Pro 模型（可选）
  base_url: https://api.deepseek.com
  max_iterations: 8                # ReAct 最大迭代次数
embedding:
  api_key: ${LLM_API_KEY}
  base_url: https://ark.cn-beijing.volces.com
  model: doubao-embedding
  enable_history_search: false
```

- `APP_ENV=dev` 使用 SQLite，`APP_ENV=prod` 使用 MySQL
- 敏感信息用 `${VAR}` 占位符，环境变量注入
- dev 环境只需 `LLM_API_KEY` 即可启动

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
| POST | `/api/auth/login` | 无 | 用户登录，获取 token |
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
| POST | `/api/conversations/:id/extract-memories` | 手动提取记忆 |

### 账单
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/bills` | 账单列表 |
| GET | `/api/bills/export` | 导出 CSV |
| POST | `/api/bills/import` | 导入 CSV |

### 笔记
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/notes` | 笔记列表 |
| POST | `/api/notes/batch` | 批量获取笔记 |
| DELETE | `/api/notes/:id` | 删除笔记 |

### 记忆
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/memories` | 记忆列表 |
| POST | `/api/memories/batch` | 批量获取记忆 |
| DELETE | `/api/memories/:id` | 删除记忆 |
| POST | `/api/memories/batch-delete` | 批量删除记忆 |

### 智能体
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/agents` | 创建智能体 |
| GET | `/api/agents` | 智能体列表 |
| GET | `/api/agents/:id` | 智能体详情 |
| PATCH | `/api/agents/:id` | 更新智能体 |
| DELETE | `/api/agents/:id` | 删除智能体 |

### 动态工具
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/tools` | 工具列表 |
| POST | `/api/tools` | 创建动态工具 |
| PATCH | `/api/tools/:id` | 更新工具 |
| DELETE | `/api/tools/:id` | 删除工具 |

### System Prompt
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/prompts` | prompt 列表 |
| POST | `/api/prompts` | 创建 prompt |
| POST | `/api/prompts/:id/rollback` | 回滚到指定版本 |
| DELETE | `/api/prompts/:id` | 删除 prompt |

## API 错误码

统一响应格式 `{"status":"ok","errorCode":0,"errMsg":"","data":{}}`（待实施）。

| 码段 | 方向 | 说明 |
|------|------|------|
| **1xxx** | 用户 & 认证 | 未登录、token 过期、注册冲突等 |
| **2xxx** | 数据 & 资源 | 资源不存在、参数校验、权限 |
| **3xxx** | LLM & AI | 模型调用失败、返回异常、超时 |
| **4xxx** | 系统 & 基础设施 | 数据库错误、配置错误、内部异常 |

| 错误码 | 说明 |
|--------|------|
| 1001 | 未登录 / token 缺失 |
| 1002 | token 无效或已过期 |
| 1003 | 用户名已存在 |
| 1004 | 注册功能已禁用 |
| 2001 | 请求的资源不存在 |
| 2002 | 请求参数缺失或格式错误 |
| 2003 | 无权限访问该资源 |
| 3001 | LLM API 调用失败 |
| 3002 | LLM 返回数据异常 |
| 3003 | LLM 请求超时 |
| 4001 | 数据库操作失败 |
| 4002 | 服务器内部错误 |

## SSE 事件类型

| event | 触发时机 |
|-------|----------|
| `text_delta` | LLM 回复文本，逐字推送 |
| `thought_delta` | 思考模式思维链，逐字推送 |
| `thought_done` | 思考完成 |
| `tool_call` | Agent 检测到工具调用 |
| `tool_result` | 工具执行完成 |
| `plan_node_start` | 工具执行开始（plan 可视化） |
| `plan_node_done` | 工具执行完成（plan 可视化） |
| `plan` | 本轮所有工具调用汇总（DAG 图） |
| `status` | 状态通知（记忆关联等） |
| `done` | 本轮对话结束 |
| `error` | 异常 |

## 协作规范

- **修改代码前必须先说明方案，征得用户同意后才能动手**
- **提交代码前必须征得用户同意，不得擅自提交**
- 优先用 Edit 工具做小改动，避免重写整个文件

## 开发约定

- 新增功能先在 `docs/design/` 写计划文档，确认后开发
- 编码完成后必须 `make test` 全绿
- model/ 只放结构体，DB 操作放 service/
- 工具实现放 tool/，不要耦合到 controller 或 agent
- 从数据库列表拉取数据时，一定不允许拉取所有数据，必须做分页
- 每一次修改代码之前，必须给用户讲清楚改什么，怎么改，用户同意之后才能修改
- 遇到相似的功能，要抽取公共组件。
