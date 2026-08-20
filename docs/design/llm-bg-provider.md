# 双 LLM 供应商分流：主链路原厂 + 后台任务硅基流动

> 目标：在线流式聊天（及聊天同步链路）用 deepseek 原厂（快、贵）；所有后台/异步 LLM 调用（记忆提取、摘要、画像、主题、反思、任务、提醒等）走硅基流动（慢、便宜）。成本中心无需改代码。

## 0. 背景知识

- **客户端创建**：`server/internal/app/init_llm.go` 的 `InitLLM()` 用两个工厂函数（`newClient`/`newStrongClient`，实际实现是 `server/internal/llm/factory.go` 的 `llm.NewClient`/`llm.NewStrongClient`）创建 Flash + Pro 两个客户端，存入 `app.LLMClients{Flash, Strong}`。
- **客户端与供应商无关**：`provider: deepseek` 分支创建的是 `FlashClient`/`ProClient`（`server/internal/llm/deepseek.go`），它们只读 `api_key/base_url/model` 三个字段，走 OpenAI 兼容协议。硅基流动也是 OpenAI 兼容协议，所以复用 `provider: deepseek` 分支、只改 `base_url` 和模型名即可，不需要给 factory 加新 provider 分支。
- **worker 不自带客户端**：所有后台 worker（MemoryExtractor、SummaryWorker、ProfileWorker、Scorer、Secretary、ThemeAnalyst、ThemeDiscoverer、StateUpdater、MissionRunner、MissionDiscoverer、ReminderTool、ReflectionOrchestrator）的 LLM 客户端都是构造时从外部注入的。注入点集中在 3 个文件：`appinit/tools.go`、`appinit/agents.go`、`appinit/router.go`。换供应商只需改这 3 个文件的注入点，不修改任何 worker 内部代码。
- **成本中心**：`llm_cost_details.model` 记录每次调用的 `ModelName()`（即配置里的模型名字符串），定价表 `llm_model_prices` 按「模型名 + 生效日期」匹配。硅基模型名不同 → 天然分开计价，零代码改动，只需在管理页录入价格行。
- **回退约定**：`llm_bg` 未配置（`model` 为空字符串）时，后台客户端 = 主客户端，行为与之前完全一致。未配置 `llm_bg` 的环境（dev/test）零影响。

## 1. 配置

### 1.1 `server/internal/config/config.go`

- `Config` 结构体加字段：`LLMBg LLMConfig \`yaml:"llm_bg"\``（复用 LLMConfig 类型，零新结构体）。
- `merge()` 增加 LLMBg 的 7 个字段合并（Provider/APIKey/Model/StrongModel/BaseURL/HTTPTimeoutSecs/StrongHTTPTimeoutSecs）。
- `resolveEnvVars()` 增加 `cfg.LLMBg.APIKey = os.ExpandEnv(...)`。
- `validate()`：`cfg.LLMBg.Model != ""` 时给 HTTPTimeoutSecs/StrongHTTPTimeoutSecs 补默认值（120/600）。否则 FlashClient 会拿到 `http.Client{Timeout: 0}`（永不超时）。
- **不校验 bg 的 api_key 必填**——空 = 未启用，是合法状态。

### 1.2 `server/conf/config.yaml`

新增 `llm_bg:` 段（model/strong_model 留空，`api_key` 用 `${LLM_BG_API_KEY}` 环境变量）。dev/test 不配；prod 启用时在 `config.prod.yaml` 填模型名。

## 2. 客户端创建

### 2.1 `server/internal/app/llm.go`

`LLMClients` 增加 `BgFlash`、`BgStrong` 字段。

### 2.2 `server/internal/app/init_llm.go`

`InitLLM` 内：`cfg.LLMBg.Model != ""` 时用同一工厂 `newClient(cfg.LLMBg)` / `newStrongClient(cfg.LLMBg)` 创建 bg 对，同样包 monitor（名字 `background/flash`、`background/pro`）+ costTracker；否则 `bgFlash, bgStrong := llmClient, strongClient`（回退）。

### 2.3 `server/cmd/xhan-helper/main.go`

`llmClients.Flash/Strong` 的 `WrapIOAudit` 之后，追加 `BgFlash`、`BgStrong` 两行。回退场景（Bg 与主同实例）也安全：两条线路各包各的 audit 装饰器，同一次调用只走其中一条路，不会双记。

## 3. 接线换源

| 文件 | 换 BgFlash | 换 BgStrong | 保持原厂 |
|------|-----------|-------------|---------|
| `appinit/tools.go` | MemExtractor、Scorer、Secretary、ThemeDiscoverer、ReminderTool | ThemeAnalyst | — |
| `appinit/agents.go` | MissionRunner、ReflectionOrchestrator.FlashClient、StateUpdater、SummaryWorker、ProfileWorker.Flash | MissionDiscoverer、ProfileWorker.Strong | Orchestrator（re_act_loop/verify/title/sub-agent）、ReplayRunner |
| `appinit/router.go` | AgentHandler（agent_summary） | — | eval handler（admin 交互）用 Flash |

- ThemeDiscoverer 收 `*app.LLMClients`，传 `&app.LLMClients{Flash: BgFlash, Strong: BgStrong}`。
- `InitRouter` 签名 `llm.LLMClient` → `*app.LLMClients`；函数体内两处（AgentHandler、EvalAdminHandler）都要改，main.go 调用处同步改。
- 子 agent（sub-agent）固定在 orchestrator 的 `o.flashClient`，保持原厂（聊天同步链路，延迟敏感）。

## 4. 测试

`server/internal/app/init_test.go`：
- `TestInitLLM` 追加 Bg 回退断言（未配 `llm_bg` 时 `BgFlash == Flash`、`BgStrong == Strong`）。
- 新增 `TestInitLLM_BgConfigured`：配置 `LLMBg` 后 Bg 为独立包装客户端。

## 5. 验收

在 `server/` 目录：

1. `go build ./...` 通过
2. `go test ./...` 全绿（不要跑 `make test`——该脚本因测试数据库残留有既有失败，与改动无关）
3. 真机验证（bg 已配置）：
   - 正常聊天一次：主链路在 monitor 的 `orchestrator/flash`，聊天后记忆提取/摘要等在 `background/flash`，无 `[post_chat_group]` 报错
   - Admin 成本中心：同时出现原厂模型名（main_chat 类）与硅基模型名（post_chat/reflection 类）
   - 未配置 bg 的环境：日志只有 `orchestrator/flash`，一切如旧

## 6. 严禁清单

- 不给 `factory.go` 加 `"siliconflow"` provider 分支
- 不改任何 worker 内部代码
- 不动 OrchestratorConfig / ReplayRunnerConfig 的 client 字段
- 不改 `LLMConfig` 结构体本身
- 不给 bg 的 api_key 加必填校验
- 不在 main.go 给 bg 加 `if != nil` 条件包裹

## 7. 上线运维（成本中心，纯页面操作）

1. 聊天一两次，让成本中心出现硅基模型名的消耗记录
2. 管理页「成本中心 → 模型定价」给硅基模型名（含 strong 模型）录价格行，生效日期填启用日；原厂价格行保留不删
3. 之后按需在 `config.prod.yaml` 填 `llm_bg.model/strong_model` 重启生效；想切回单供应商就把 `llm_bg.model` 置空

## 风险提示

- 硅基 cache hit/miss 字段若与 deepseek 不同名，成本中心该两列记 0，不影响金额正确性
- bg HTTP 超时单独可配 `llm_bg.http_timeout_secs`，硅基慢时可调大
- 后台任务全异步（session_end/cron），硅基变慢不影响聊天体验