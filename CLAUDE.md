# xhanHelper — 个人 AI Agent 助手

## 项目概述

以聊天为主要交互方式的个人 AI Agent，SwiftUI 原生客户端 (iOS/macOS) + Go 单体后端。

核心能力：用户通过自然语言对话，Agent 自主判断意图、调用工具（记账/爬虫等）完成任务。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.22+, Gin, GORM, SQLite(dev)/MySQL(prod) |
| AI | 豆包 API (ark.cn-beijing.volces.com), LLM 抽象层可切换 |
| 客户端 | SwiftUI (iOS + macOS), 待开发 (P2) |
| 通信 | SSE (流式对话) + HTTP REST |

## 目录结构

```
xhanHelper/
├── server/                    # Go 后端 (MVC + Agent)
│   ├── cmd/xhan-helper/main.go
│   ├── conf/                  # config.yaml + config.dev/prod.yaml
│   ├── internal/
│   │   ├── config/            # YAML 加载 + 环境合并
│   │   ├── model/             # M: GORM 模型
│   │   ├── service/           # M: DB Repo 层
│   │   ├── controller/        # C: Gin handlers
│   │   ├── view/              # V: SSE / JSON 渲染
│   │   ├── middleware/        # Device Token 认证
│   │   ├── llm/               # LLM 抽象层 (接口+工厂+实现)
│   │   ├── agent/             # ReAct 循环编排器
│   │   └── tool/              # 工具接口 + 注册表 + 实现
│   ├── scripts/api-test.sh    # API 自动化测试 (27 项)
│   └── Makefile               # dev/test/build/clean
├── client/                    # SwiftUI (P2)
├── scripts/                   # curl 测试脚本
└── design/                    # 产品文档 + 各阶段计划
```

## MVC + Agent 架构

```
HTTP Request
  → middleware/auth.go          设备认证, 注入 device 到 context
  → controller/chat.go          解析参数, 调用 Orchestrator
  → agent/orchestrator.go       ReAct 循环 (最多 5 轮)
     ├── llmClient.ChatSync()   调用豆包 /responses API
     ├── parseLLMResponse()     解析 JSON {reply, tool_calls}
     └── registry.ExecuteByName()  执行工具
  → service/*_repo.go           DB CRUD
  → view/render.go              SSE / JSON 输出
```

## LLM 调用方式

豆包 API (`https://ark.cn-beijing.volces.com/api/v3/responses`) 不支持原生 function calling。

Agent 通过 prompt engineering 实现工具调用：
- System prompt 注入工具定义和输出格式约定
- LLM 返回结构化 JSON：`{"reply":"...","tool_calls":[{...}]}`
- Orchestrator 解析 → 执行工具 → 结果回传 → 循环

LLM 抽象层 (`llm/adapter.go`)：`LLMClient` 接口 + `NewClient(provider)` 工厂。
当前 provider=doubao，可切换 claude/deepseek（需新增实现）。

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

添加新工具：实现接口 → `reg.Register(mytool)` → 无需改其他代码。

当前工具：`bookkeeping` (create/update/delete/query 4 个 function)

## 配置

- 敏感信息 (API Key, DB 密码) 用 `${VAR}` 占位符，通过环境变量注入
- `APP_ENV=dev` 使用 SQLite（免装 MySQL），`APP_ENV=prod` 使用 MySQL
- dev 环境只需 `LLM_API_KEY` 一个环境变量即可启动

## 常用命令

```bash
cd server
make dev          # 启动开发服务器 (检查 LLM_API_KEY)
make test         # 一键 API 自动化测试 (编译→启动→27项测试→停止)
make build        # 编译生产二进制
make clean        # 清理
```

## API 接口

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/api/devices/register` | 无 | 注册设备，获取 token |
| POST | `/api/chat` | Bearer | SSE 流式对话 (含 tool_call/tool_result) |
| GET | `/api/conversations` | Bearer | 会话列表 |
| GET | `/api/conversations/:id/messages` | Bearer | 消息历史 |
| GET | `/api/bills` | Bearer | 账单列表 |

## SSE 事件类型

| event | 触发时机 |
|-------|----------|
| `message` | LLM 回复文本，逐字推送 |
| `tool_call` | Agent 检测到工具调用 |
| `tool_result` | 工具执行完成 |
| `done` | 本轮对话结束 |
| `error` | 异常 |

## 开发约定

- Go 文件不放 docstring，不放无用注释
- 新增功能先在 `design/` 写计划文档，确认后开发
- 编码完成后必须 `make test` 全绿
- model/ 只放结构体，DB 操作放 service/
- 工具实现放 tool/，不要耦合到 controller 或 agent
