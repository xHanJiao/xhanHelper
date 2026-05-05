# 动态工具系统

> 日期: 2026-05-05 | 状态: 方案设计

## 目标与背景

当前系统：工具在 Go 代码中硬编码（bookkeeping/datetime/uuid/calculator/note）。新增工具需要改 Go 代码 → 编译 → 重启服务。LLM 无法自主扩展能力。

目标：让 LLM 在对话中自主发现工具不足、提出新工具需求、生成工具代码、验证后投入使用。

## 核心原则

- **安全第一**：所有代码必须经用户审阅后才能进入生产环境
- **小步快跑**：先做最小闭环（用户手动创建工具 → Python 执行），再逐步开放（LLM 提议 → 外部 CLI 生成 → 验证）
- **动态加载，永不重启**：Go 从 DB 读工具注册表，Python sidecar 热加载脚本
- **不开放 DDL**：工具只能读写 `tool_data` 表，不能创建/修改表结构

---

## 整体架构

```
┌─────────────────────────────────────────────────┐
│                    Go 服务                        │
│                                                   │
│  orchestrator → tool.Registry(DB) → 查 schema     │
│              → tool_data(DB)    → 查缓存(input_hash)│
│              → os/exec stdio   → 调用 sidecar     │
│              → tool_data(DB)   → 写结果           │
│                                                   │
│  ┌─────────────┐     ┌──────────────┐             │
│  │  prod.py    │     │ validate.py  │             │
│  │  (长连接)   │     │  (按需启动)  │             │
│  │  MCP/stdio  │     │  MCP/stdio   │             │
│  └─────────────┘     └──────────────┘             │
│       │                    │                      │
│  tools/prod/         tools/validate/              │
│  (审阅过的脚本)      (待验证脚本，每次只跑一个)    │
└─────────────────────────────────────────────────┘
```

### 为什么用 Python + MCP 协议

- **Python**：解释型语言，可以热加载（`importlib.reload`），Go 不需要重启
- **MCP 协议**：基于 stdio 的 JSON-RPC，Go 通过 `os/exec` 启动子进程，管道通信。无需网络端口、无需 Docker
- **安全**：子进程跑在独立工作目录，无法访问 `.env`、数据库文件、其他用户数据。超时 30s + 内存限制 256MB

### 为什么不用 Docker

服务器 2C2GiB，当前内存占用 53%，跑 Docker 太紧张。`os/exec` + `syscall.Setrlimit` 的资源隔离已经足够。

---

## 存储设计

### tool_registry — 工具注册表（永久，存 schema）

```sql
CREATE TABLE tool_registry (
    id          VARCHAR(36) PRIMARY KEY,
    name        VARCHAR(100) NOT NULL,              -- 工具名（如 "weather"）
    description TEXT NOT NULL,                      -- 工具描述
    schema_json JSON NOT NULL,                      -- 函数定义数组 [{"name":"get_weather","description":"...","parameters":{...}}]
    script_path VARCHAR(500) NOT NULL DEFAULT '',   -- Python 脚本路径 (如 "tools/prod/weather.py")
    enabled     TINYINT(1) NOT NULL DEFAULT 1,     -- 是否启用
    user_id     VARCHAR(36) NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_name (name),
    KEY idx_user (user_id)
);
```

Go 侧定期（30s TTL 缓存）或按需从 DB 读取 `enabled=1` 的工具，构造 `[]ToolDefinition` 传给 LLM。

### tool_data — 调用记录（每次调用一条，支持缓存）

```sql
CREATE TABLE tool_data (
    id           VARCHAR(36) PRIMARY KEY,
    tool_name    VARCHAR(100) NOT NULL,
    func_name    VARCHAR(100) NOT NULL,
    input_json   JSON NOT NULL,                     -- 原始输入参数
    input_hash   VARCHAR(64) NOT NULL,              -- MD5(input_json)，用于缓存命中
    output_json  JSON NOT NULL,                     -- 输出结果
    user_id      VARCHAR(36) NOT NULL,
    cached       TINYINT(1) NOT NULL DEFAULT 0,     -- 是否可缓存（纯函数=true，有副作用=false）
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    KEY idx_input_hash (tool_name, func_name, input_hash),
    KEY idx_user (user_id)
);
```

**缓存逻辑**：新调用时先查 `WHERE tool_name=? AND func_name=? AND input_hash=? AND cached=1`，命中直接返回 `output_json`。用于纯函数（如 `calculator`、`uuid.generate`），副作用函数（如 `bookkeeping.create_bill`）标记 `cached=0`。

---

## 工具调用完整流程

```
1. 用户消息 → orchestrator
2. buildSysPrompt:
   a. 从 tool_registry(DB) 读 enabled=1 的工具列表（30s 缓存）
   b. 和现有硬编码工具合并 → 生成 tool definitions
3. LLM 返回 tool_call
4. orchestrator 解析 tool_call:
   a. 先查 tool_data.input_hash — 命中缓存 → 直接返回 output_json
   b. 未命中:
      - 如果工具在硬编码注册表中 → 现有流程（Go 直接执行）
      - 如果工具在 tool_registry 中 → 通过 stdio 向 prod.py 发 MCP 请求
        prod.py: import 对应脚本 → 执行 → 返回 JSON
   c. 写 tool_data 记录 (input + output + hash)
5. 结果返回 LLM，继续对话
```

---

## 双 Sidecar 安全模型

### 生产 sidecar (prod.py)

- 长连接 stdio 进程，Go 启动后保持运行
- 只加载 `tools/prod/` 目录下的脚本（用户已审阅）
- 支持热加载：检测到新文件或文件修改 → `importlib.reload`

### 验证 sidecar (validate.py)

- 按需启动，每次只运行一个待验证的脚本
- 加载 `tools/validate/` 目录下的脚本
- 执行完立刻退出，超时 30s 强杀
- 用户审阅通过后，脚本从 `tools/validate/` 移到 `tools/prod/`

### 安全清单

| 层级 | 措施 |
|------|------|
| 进程隔离 | `os/exec` 子进程，独立工作目录 |
| 文件系统 | 子进程无法访问项目 `.env`、`conf/`、数据库文件 |
| 网络 | 子进程无网络访问（netns 或 host 防火墙） |
| 资源 | `Cmd.Timeout` 30s, `syscall.Setrlimit` 256MB 内存 |
| CPU | `nice` 低优先级，防止占满 CPU |
| 代码 | 必须用户审阅后才从 validate → prod |
| 数据库 | 工具只能通过 Go 提供的接口读写 `tool_data`，不直接连 DB |
| 无 DDL | 工具无法执行 CREATE/ALTER/DROP |

---

## 工具创建流程

### 最小闭环（第一步实现）

```
用户手动创建:
  1. 写 Python 脚本 → 放入 tools/validate/
  2. 用户审阅代码
  3. 通过 → 移到 tools/prod/
  4. 在 tool_registry 表中 INSERT 一条注册记录
  5. prod.py 自动检测并加载 → 工具可用
  6. LLM 下次对话可调用
```

### 半自动（第二步）

```
LLM 提议:
  1. LLM 在对话中发现工具不足 → 向用户提出需求
  2. 用户与 LLM 讨论确定工具的输入/输出/功能
  3. LLM 生成 Python 代码 → 写入 tools/validate/
  4. validate.py 试运行，Go 侧检查结果格式
  5. 用户审阅 → 通过后移到 prod/ + INSERT tool_registry
```

### 全自动（第三步，远期）

```
LLM 自主:
  1. LLM 提议新工具 → 调用外部 CLI（claude-code / opencode）生成代码
  2. 自动放入 tools/validate/ → 自动试运行
  3. 结果报告给用户 → 用户一键审阅通过 → 自动迁移到 prod
```

---

## 迁移计划

当前硬编码工具（bookkeeping/datetime/uuid/calculator/note）可选迁移到 `tool_registry` + `tool_data`：

- 迁移价值高的：calculator、uuid（纯函数，可享受缓存加速）
- 暂不迁移的：bookkeeping、note（逻辑复杂，重写成本高）

迁移不需要一次性全做，逐步来。

---

## 与现有工具的共存

初期两种工具并存：
- **硬编码工具**（Go）：bookkeeping, datetime, uuid, calculator, note — 保持不变
- **动态工具**（Python via DB）：从 tool_registry 加载 — 新工具走这条路径

LLM 看到的工具列表是两者合并。
