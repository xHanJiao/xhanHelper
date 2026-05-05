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
┌─────────────────────────────────────────────────────┐
│                    Go 服务                            │
│                                                       │
│  orchestrator → tool.Registry(DB) → 查 schema         │
│              → tool_data(DB)      → 查缓存(input_hash)│
│              → http.Post(:9090)   → 调用 sidecar      │
│              → tool_data(DB)      → 写结果            │
│                                                       │
│  ┌─────────────────┐   ┌────────────────┐             │
│  │  prod.py        │   │ validate.py    │             │
│  │  (常驻)         │   │  (按需启动)    │             │
│  │  MCP HTTP :9090 │   │  MCP HTTP :9091│             │
│  └─────────────────┘   └────────────────┘             │
│       │                      │                        │
│  tools/prod/           tools/validate/                │
│  (审阅过的脚本)        (待验证脚本，每次只跑一个)      │
└─────────────────────────────────────────────────────┘
```

### 通信方式：MCP Streamable HTTP

最终选择 **HTTP** 而非 stdio，原因：
- orchestrator 已支持 `executeConcurrently` 并发执行工具
- stdio 是单工通道，多 goroutine 需排队，成为瓶颈
- HTTP 天然支持并发，每个 goroutine 独立发请求

Python sidecar 使用 [MCP Streamable HTTP](https://spec.modelcontextprotocol.io/specification/2025-03-26/basic/transports/#streamable-http) 传输模式：

```
Python sidecar: 监听 127.0.0.1:9090
  POST /mcp  {"jsonrpc":"2.0","method":"tools/call","params":{...}}
  ← 完全符合 MCP 协议
  ← HTTP 天然支持并发
  ← 未来开源 MCP server 可直接接入，零转换
```

Go 侧 `http.Post("http://127.0.0.1:9090/mcp", ...)`，端口只绑 127.0.0.1，外网无法访问。

### 部署方式：systemd 管理，两服务并列

Go 不需要管理 Python 进程。两个服务由 systemd 并列管理：

```
systemd:
  xhan-helper.service      (Go, :8088)
  xhan-sidecar-prod.service (Python, :9090, 常驻)
  xhan-sidecar-val.service  (Python, :9091, 按需启停)
```

Go 只管通过 HTTP 调用 sidecar，不关心进程死活（systemd 负责重启）。

### 为什么用 Python

- **解释型语言**：可以热加载（`importlib.reload`），无需重启
- **独立进程**：systemd 隔离工作目录，无法访问 `.env`、数据库文件

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
    status      VARCHAR(20) NOT NULL DEFAULT 'draft', -- draft / active / disabled / retired
    user_id     VARCHAR(36) NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_name (name),
    KEY idx_user (user_id),
    KEY idx_status (status)
);
```

### 状态流转

```
draft → active → disabled → active  (可反复启用/禁用)
  │                 │
  └──→ retired ←────┘
```

- **draft**：新创建，待审阅，不可被 LLM 调用
- **active**：审阅通过，LLM 可见可调用
- **disabled**：临时关闭（如发现 bug），LLM 不可见
- **retired**：永久废弃，不可恢复

Go 侧定期（30s TTL 缓存）从 DB 读取 `status='active'` 的工具。

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

## Go 实现计划（Phase 4 第一步）

### 新增文件

| 文件 | 说明 |
|------|------|
| `model/tool_registry.go` | `ToolRegistry` + `ToolData` 结构体 |
| `service/tool_repo.go` | tool_registry 的 CRUD + tool_data 的缓存查找/写入 |
| `controller/tools.go` | 管理 API：注册/列表/删除工具 |
| `tools/prod/sidecar.py` | 生产 sidecar — MCP HTTP server，热加载工具脚本 |

### 改动既有文件

| 文件 | 改动 |
|------|------|
| `migrations/001_init.sql` | 加 `tool_registry` + `tool_data` 表 |
| `tool/registry.go` | `GetLLMTools()` 合并 DB 动态工具 |
| `tool/tool.go` | 加 `DynamicTool` 实现 `Tool` 接口，Execute 调 HTTP sidecar |
| `orchestrator.go` | 工具执行前查 `tool_data` 的 `input_hash` 缓存 |
| `router.go` | 注册 tools API |

### 工具调用完整流程

```
1. LLM 返回 tool_call
2. orchestrator 解析 → executeConcurrently (goroutine pool)
3. 每个 goroutine:
   a. MD5(args) → 查 tool_data.input_hash
      → 命中 + cached=1 → 直接返回 output_json
   b. 未命中:
      → 组装 MCP JSON-RPC 请求
      → http.Post("http://127.0.0.1:9090/mcp", jsonBody)
      → 30s 超时
      → 写 tool_data (input + output + hash + cached flag)
   c. 结果返回 LLM
```

### 管理 API

```
POST   /api/tools              — 注册新工具 (INSERT tool_registry, status=draft)
GET    /api/tools?status=active — 工具列表，支持按状态筛选
GET    /api/tools/:id           — 工具详情
PATCH  /api/tools/:id           — 更新注册信息（名称/schema/script_path/status）
DELETE /api/tools/:id           — 删除注册
POST   /api/tools/:id/reload    — 通知 sidecar 热重载
```

---

---

## Python 侧设计

### 目录结构

```
tools/
├── prod/                      # 生产环境
│   ├── sidecar.py             # MCP HTTP server 主进程
│   ├── tools/                 # 工具脚本目录
│   │   ├── __init__.py
│   │   ├── base.py            # Tool 基类
│   │   └── calculator.py      # 示例：计算器
│   ├── requirements.txt
│   └── venv/                  # 独立虚环境
├── validate/                  # 验证环境（独立 venv）
│   ├── sidecar.py
│   ├── tools/
│   ├── requirements.txt
│   └── venv/
└── systemd/
    ├── xhan-sidecar-prod.service
    └── xhan-sidecar-val.service
```

**虚环境策略**：
- prod 和 validate 各有独立 venv，验证环境装坏包不影响生产
- 所有工具共用一个 venv（个人助手 10-20 个工具，依赖冲突概率极低）
- 不做每工具独立 venv（过度设计）
- 新依赖需要重启 sidecar（`importlib.reload` 只热加载脚本，不加载新 site-packages）

### sidecar.py — MCP HTTP Server

核心职责：监听 `POST /mcp`，解析 JSON-RPC 请求，路由到对应工具执行。

```python
# tools/prod/sidecar.py
from flask import Flask, request, jsonify
from tools.loader import ToolLoader

app = Flask(__name__)
loader = ToolLoader("tools/prod/tools")  # 加载所有工具
loader.watch()                            # 启动文件监控，自动热重载

@app.post("/mcp")
def handle_mcp():
    body = request.get_json()
    # body: {"jsonrpc":"2.0","id":"1","method":"tools/call","params":{...}}
    if body.get("method") == "tools/list":
        return jsonify(loader.list_tools())
    if body.get("method") == "tools/call":
        result = loader.execute(body["params"])
        return jsonify({"jsonrpc":"2.0","id":body["id"],"result":result})
    # 其他 MCP 方法: initialize, ping 等

@app.post("/reload")
def reload():
    loader.reload()
    return jsonify({"ok": True})

if __name__ == "__main__":
    app.run(host="127.0.0.1", port=9090)
```

### Tool 基类 — tools/base.py

每个工具文件定义一个类，继承 `BaseTool`：

```python
# tools/prod/tools/base.py
import json
from typing import Any

class ToolResult:
    def __init__(self, success: bool, data: Any = None, error: str = None):
        self.success = success
        self.data = data
        self.error = error

class BaseTool:
    name: str = ""            # 工具名
    description: str = ""     # 工具描述

    def functions(self) -> list[dict]:
        """返回函数 schema 列表"""
        raise NotImplementedError

    def execute(self, func_name: str, args: dict) -> ToolResult:
        """执行指定函数"""
        raise NotImplementedError
```

### 示例工具 — tools/calculator.py

```python
# tools/prod/tools/calculator.py
from .base import BaseTool, ToolResult

class CalculatorTool(BaseTool):
    name = "calculator"
    description = "数学计算工具"

    def functions(self):
        return [{
            "name": "evaluate",
            "description": "计算数学表达式",
            "parameters": {
                "type": "object",
                "properties": {
                    "expression": {"type": "string", "description": "数学表达式"}
                },
                "required": ["expression"]
            }
        }]

    def execute(self, func_name, args):
        if func_name == "evaluate":
            try:
                result = eval(args["expression"], {"__builtins__": {}}, {})
                return ToolResult(success=True, data={"result": result})
            except Exception as e:
                return ToolResult(success=False, error=str(e))
        return ToolResult(success=False, error=f"unknown function: {func_name}")
```

### ToolLoader — 动态加载 + 热重载

```python
# tools/prod/tools/loader.py
import importlib, os, time, threading
from watchdog.observers import Observer
from watchdog.events import FileSystemEventHandler

class ToolLoader:
    def __init__(self, tools_dir):
        self.dir = tools_dir
        self.tools = {}
        self._load_all()

    def _load_all(self):
        for f in os.listdir(self.dir):
            if f.endswith(".py") and not f.startswith("_"):
                mod = importlib.import_module(f"tools.{f[:-3]}")
                for attr in dir(mod):
                    obj = getattr(mod, attr)
                    if isinstance(obj, type) and issubclass(obj, BaseTool) and obj != BaseTool:
                        inst = obj()
                        self.tools[inst.name] = inst

    def reload(self):
        self.tools = {}
        self._load_all()

    def list_tools(self):
        return [{"name": t.name, "functions": t.functions()} for t in self.tools.values()]

    def execute(self, params):
        tool = self.tools.get(params["tool"])
        if not tool:
            return {"success": False, "error": f"unknown tool: {params['tool']}"}
        r = tool.execute(params["function"], params.get("args", {}))
        return {"success": r.success, "data": r.data, "error": r.error}

    def watch(self):
        # watchdog 监控文件变更，自动调用 reload()
        ...
```

### 职责边界

```
Python 侧（全部隔离逻辑）
  ├── 接收工具脚本上传
  ├── validate sidecar 试运行
  ├── 审阅通过 → 移动脚本 validate→prod
  ├── 通知 prod sidecar reload
  └── 更新 DB tool_registry.status = 'active'

Go 侧（不感知隔离）
  ├── GET /api/tools → 管理 DB 记录
  ├── GetLLMTools() → 拉取 status=active 的工具给 LLM
  └── DynamicTool.Execute → HTTP 调 prod sidecar 执行
```

Go 不关心脚本在哪个目录、怎么迁移。未来这个迁移逻辑可以拆成独立微服务。

### 新增工具的流程

```
1. 写一个 <tool_name>.py，继承 BaseTool，实现 functions() + execute()
2. 放入 tools/prod/tools/
3. POST /reload 或等待文件监控自动重载
4. 在 tool_registry 表中 INSERT 一条记录（status='draft'）
5. 审阅通过 → status='active' → LLM 可调用
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
