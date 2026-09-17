# xhanHelper — 个人 AI Agent 助手

以聊天为主要交互方式的个人 AI Agent：Go 单体后端 + Vue 3 前端 + Swift 客户端（macOS/iOS）。
用户用自然语言对话，Agent 自主判断意图、调用工具（记账/笔记/文档/检索/提醒/子任务等）完成任务。
具备向量记忆检索、渐进式工具暴露、自定义智能体、后台后处理流水线（记忆/摘要/画像/状态页）与反思投递系统。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go **1.25**、Gin、GORM、MySQL（dev/prod）、SQLite（**仅 test**） |
| AI | provider: `deepseek` / `ark` / `doubao`（`claude` 为 stub）；DeepSeek 原生 function calling；Ark Embedding |
| 前端 | Vue 3 + Naive UI + Pinia + Vue Router + Chart.js + markdown-it + Vite（`front-end/`）。无 Tailwind，样式用 CSS 变量 + Naive token |
| 客户端 | Swift + SwiftUI，macOS 26.3 / iOS 26.0；`Shared/` 同时编进两个 target |
| 通信 | SSE（流式对话）+ HTTP REST。生产前端由 **nginx 独立托管**，Go 不 serve `dist/` |
| 向量 | Ark Embedding API + 内存向量存储（余弦相似度 + 时间衰减） |

## 目录结构

```
xhanHelper/
├── AGENTS.md
├── server/                             # Go 后端
│   ├── cmd/                            # 4 个入口：xhan-helper（主服务）/ analyze-cache / eval-export / eval-label
│   ├── conf/                           # config.yaml（base）+ config.{dev,prod,test}.yaml
│   ├── scripts/                        # api-test.sh、client-tool-e2e.sh、control/dump 脚本
│   ├── Makefile
│   └── internal/
│       ├── config/      # YAML 加载 + APP_ENV 覆盖合并 + ${VAR} 展开 + 校验
│       ├── model/       # GORM 结构体 + 常量（只放结构体，不写 DB 操作）
│       ├── service/     # DB Repo 层（每个模型一个 *_repo.go）
│       ├── controller/  # Gin handler + 路由（router.go 为主，另有 reflection/eval/cost/llm_io/subagent 分组注册器）
│       ├── middleware/   # Bearer 认证 + 请求日志
│       ├── agent/       # 核心：orchestrator、三套 ReAct 循环、后台 worker、检索源、prompt 构建
│       │   ├── pipeline/       # 预处理管线（短输入检测/日期规范化）
│       │   ├── promptbuilder/  # System Prompt 分层构建
│       │   ├── ctxflow/        # 上下文检索 + XML 格式化
│       │   ├── retrieval/      # ContextSource 实现（memory/note/history/summary/theme…）
│       │   └── replay/         # 对话重放对比（admin 调试）
│       ├── llm/         # LLM 抽象层（deepseek/ark/doubao/claude + embedding + files 图床）
│       ├── llmcost/     # 成本中心：UsageRecorder + CostTracker 装饰器 + I/O 审计
│       ├── tool/        # 工具接口 + Registry + 内置工具
│       ├── app/         # 基础设施装配层（InitDB/InitRepos/InitInfra/InitLLM + 跨包接口）
│       ├── appinit/     # 初始化编排 + Serve（HTTP 启动/优雅关闭）
│       ├── eval/        # 检索评测框架（数据集/标注/指标）
│       ├── profilefield/# 用户画像白名单字段定义（字段即代码，人工放行）
│       ├── monitor/     # 监控看板 + Admin 中间件 + WrapLLMClient
│       ├── mcp/ async/ idgen/ util/ view/
├── front-end/                          # Vue 3
│   └── src/{views,components,composables,stores,api,router,utils}
├── client/xHelper/
│   ├── Shared/{Views,Services,ViewModels,Tools,Models,MCP,Extensions}/   # 两 target 共用
│   ├── xHelper/App.swift               # iOS @main
│   ├── xHelperMac/{App.swift,SettingsStyle.swift,ShortcutGuideView.swift} # macOS @main + 风格
│   ├── Package.swift + Tests/          # 纯逻辑文件的 SwiftPM 单测入口（swift test）
│   └── ../packages/                    # vendored ELK / Mermaid 包（勿改）
├── docs/                               # design/ agents/ bugs/ front-end/ learning/ research/ security/ todo/
└── .opencode/ .claude/                 # 本地 agent 工作区（已 gitignore，非项目代码）
```

## 架构：一次聊天的链路

```
POST /api/chat
 → middleware/auth.go              Bearer Token → user_id（注入 ctx）
 → controller/chat.go              解析参数、载入/新建会话、plan 模式提示、注册 chat stop channel
 → agent/pipeline                  ShortInputCheckStep（短输入跳检索）+ NormalizeDatesStep（相对日期→绝对）
 → agent/orchestrator.go::Run      embed「原始消息」→ promptbuilder.Build → ctxflow.Retrieve
 → agent/promptbuilder             分层拼 system prompt（行为准则 / 角色定义 / 工具目录+定义 / 稳定上下文）
 → agent/ctxflow                   各 ContextSource 并发检索 → 格式化为 XMLContext，注入「当前 user 消息」
 → llm.Chat（流式）                use_native_fc 时走原生 function calling，否则解析文本 JSON 兜底
 → 工具执行                        服务端工具并发（tool_max_concurrency）、客户端工具串行
 → 消息落库 + view.WriteSSE        断开连接不终止执行（runCtx 独立于 Request.Context）
```

硬约束：**进入请求体的动态内容必须字节稳定**（工具定义/目录按工具名排序），否则破坏 DeepSeek 前缀缓存。
动态内容（检索上下文/画像/摘要）一律注入当前 user 消息，**不放进 system prompt**。

## 三套 ReAct 循环（改循环前先确认改的是哪一套）

| 循环 | 文件 | 调用方式 | 用在哪 |
|------|------|----------|--------|
| 主编排循环 | `agent/orchestrator.go` | 流式 `Chat`，原生 FC 优先，最终轮强制收敛 | `POST /api/chat` 用户主对话 |
| `ReactLoop` | `agent/react_loop.go` | `ChatSync` + prompt engineering，工具结果以 `role=user` 回填 | 反思子系统（ThemeAnalyst / ThemeDiscoverer / StateUpdater / MissionRunner），写 `ReflectionTrace` |
| `MiniReAct` | `agent/mini_react.go` | `ChatSync` + 原生 FC，结果以 `role=tool` 回填，主循环耗尽后强制收敛轮 | 后台 worker 与 subagent（`task` 工具），事件前缀 `mini_react:*` |

## 数据模型（`server/internal/model/`，按域分组）

- 认证：`User`、`UserToken`
- 对话：`Conversation`（含 parent_id 子会话、mode `work|plan`）、`Message`（reasoning_content / tool_calls_json / request_json / response_json / Embedding / Status）、`ConversationSummary`、`ConversationPlan` + `PlanStep`、`ContentVersion`
- 内容：`Bill`、`Note`、`Document`、`DocumentShare`、`MessageShare`、`Reminder`、`Agent`
- 用户层：`UserMemory`、`UserProfile`、`UserProfileFact`、`UserPrompt`、`ImageAsset`
- 反思：`ThemeState`、`Mission`、`PendingFinding`、`MailboxItem`、`DelayedTask`、`ScheduledTask`、`ReflectionTrace` / `ReflectionLLMCall` / `ReflectionDecision`
- 向量：`Embedding`、`EmbeddingChunk`、`ConceptEmbedding`
- 基建：`ToolRegistry`、`MCPServerRecord`、`SearchUsage`、`LlmCostDetail`、`LlmModelPrice`、`LLMCallIO`

## LLM 调用

- 接口 `llm/adapter.go`：`Chat`（流式）、`ChatSync`、`ChatWithRawJSON`、`BuildSystemPrompt`、`ModelName`
- 双模型：Flash（日常）+ strong_model Pro（深度推理/思考模式）；`llm_bg` 供后台任务复用（为空则回退主 LLM）
- **服务端是 LLM 参数的唯一事实来源**：`/api/chat` 请求体里的 `temperature` / `max_tokens` / `thinking` 会被解析但**有意不传进 `RunRequest`**（`controller/chat.go`），一律用服务端配置默认。客户端 UI 上那几个旋钮当前不生效 —— 这是设计，不是 bug，别去"修"
- 同理：全链路**没有 `finish_reason == "length"` 处理**，`max_tokens` 触顶是静默硬截断。要限制回答长度请用 prompt（如 `brief_answer` 注入 system prompt 的输出约束），不要靠调 `max_tokens`
- 装饰器链：`llmcost.CostTracker`（token 记账）+ `IOAuditWrapper`（ChatSync I/O 审计，保留 7 天）
- 两条工具调用路径：**原生 function calling**（`llm.use_native_fc`，当前开启）与 **prompt engineering**（工具定义注入 system prompt → 解析文本 JSON）

### SSE 事件（`controller/chat.go` 分发）

`message`（由内部 `text_delta` 改名，payload `{"delta":"..."}`）、`thought` / `thought_delta` / `thought_done`、
`status`、`tool_call` / `tool_result`、`plan` / `plan_node_start` / `plan_node_done` / `plan_done`、
`verify`、`subagent`、`client_tool_call`、`reminders_changed`、`done`、`error`。

前端只消费其中一部分；`subagent` / `client_tool_call` / `reminders_changed` 仅 Swift 客户端处理。

## Tool 系统

```go
type Tool interface {
    Name() string
    Description() string
    Functions() []FuncSchema
    Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result
    ReadOnlyFuncs() map[string]bool   // plan 模式只读白名单；nil = 全可写
}
```

可选接口 `CatalogProvider`（渐进式目录的组级短描述 + 高频函数）。
`Registry` 在启动时强制校验：`ValidateNativeFC`（函数名禁 `__`）、`ValidateReadOnlyFuncs`（声明必须与 `Functions()` 一致）、`ValidateCatalog`（可见工具必须实现 CatalogProvider）——违规直接 panic。

**渐进式工具暴露**：请求体只常驻 `expose_tools` 一个元工具，LLM 按需 enable 具体工具/函数；已暴露集合按窗口恢复。

| 工具 | 说明 |
|------|------|
| `expose_tools` | 唯一常驻的元工具：按函数粒度渐进暴露其他工具 |
| `bookkeeping` | 记账增改删查 + 分类/月度统计 + 分类/owner 选项 |
| `datetime` / `calculator` | 当前时间 / 安全表达式求值 |
| `note` | 笔记 CRUD + 语义检索 + 版本快照/回滚 |
| `document` | Markdown 文档 CRUD + 标签 + 窗口读（`char_offset`/`char_limit`，大文档回 outline，标题/正文有长度上限；无版本/回滚） |
| `agent` | 自定义智能体管理 + 会话绑定 |
| `unified_search` | **统一检索入口**：语义/关键词搜索，覆盖 memory / note / summary / theme / history 五域 |
| `message_search` | 历史消息按时间范围 / ID / 数量精确查（语义检索走 `unified_search`） |
| `get_summary` | 对话摘要按时间 / ID 查 |
| `get_topic_stats` / `get_topic_detail` | topic 聚类统计 / 指定 topic 下笔记与记忆全文 |
| `get_bill_stats` | 账单结构化统计（供 Mission / 反思用） |
| `theme` / `theme_search` | 主题状态页管理（create/update/close/reactivate）/ 查询 |
| `reminder` | 待办提醒创建/查询/取消/提及标记 |
| `plan` | 会话执行计划（todo）管理 |
| `task` | 子任务/子 agent：`web_explore` / `file_explore` / `user_context`，白名单双保险 |
| `webfetch` | 网页正文抓取。**HideTool，仅 subAgent 可见**，由 `webfetch.enabled` 控制 |
| `profile` | 画像稳定层只读查询。**HideTool，仅 subAgent 可见**（画像已每轮注入主循环） |
| `mailbox` | 向信箱投递消息。只注册进**反思注册表**；主循环经 `chat.tool_excludes` 隐藏 |

## 后台处理流水线

`PostChatWorkerGroup`（`agent/post_chat_group.go`）由 `CronRunner` + `DelayedTask("session_end")` + 30s ticker 驱动，统一分发：

| worker | 产物 |
|--------|------|
| `memory_worker.go` / `extractor.go` | `UserMemory` 提取 + 向量去重（阈值 `server.memory_dupe_threshold`，默认 0.85） |
| `summary_worker.go` | `ConversationSummary` 增量合并 + embedding + chunk |
| `profile_worker.go` | `UserProfileFact`（冷启动 / 增量 / 迁移，配额校验重试） |
| `state_worker.go` / `state_updater.go` | `ThemeState` 状态页增量更新 |
| `theme_*.go` | 主题发现（对话/统计两路提名）、分析、检验、chunk/embed |
| `mission_*.go` | Mission 发现与定期检查，报告投递信箱 |
| `secretary.go` | 把分析发现转成人性化提醒文案 |
| `ephemeral_destroyer.go` | 无痕会话销毁（同步软删 + 后台分批物理清理） |

向量侧：`vector_store.go`（内存 + 余弦）、`warmup.go`（启动时把 embedding 表加载进内存）、`chunk_builder.go`（句级 chunk 双路检索）。

## 配置

- 加载顺序（`internal/config`）：`conf/config.yaml` → `conf/config.<APP_ENV>.yaml`（非零值覆盖）→ `${VAR}` 展开 → 校验
- 环境 ↔ DB：**dev = MySQL**、**prod = MySQL**、**仅 test = SQLite**（`server/data/xhan_helper.db`）。`db` 配置在 base `config.yaml`，`APP_ENV` 只切换覆盖文件
- 敏感变量：`LLM_API_KEY`、`EMBEDDING_API_KEY`、`XHAN_HELPER_DB_PASSWORD`、`ALLOWED_USER`、`XHAN_HELPER_ADMIN`。本地放 `server/.env`（已 gitignore，`make dev` 会加载）
- 顶层 key：`server`（端口/注册开关/后台任务总开关/admin_users）、`db`、`llm`（含 `verification`/`debug_json`）、`llm_bg`、`embedding`、`reflection`、`retrieval`（含 `resource_zone` 预算与配额）、`chat`（窗口/摘要/tool_excludes/image_replay）、`subagent`、`webfetch`、`storage`、`share`、`archive`、`mcp`、`snowflake`、`global_default_role`
- 后台任务总开关 `server.disable_background_tasks`，关闭后不启 `CronRunner`、不跑 profile 迁移、`onChatEnd` 短路

## 常用命令

```bash
cd server
make dev            # 启动开发服务器（dev → MySQL）
make dev-full       # 后端 + 前端 dev server（:8080 / :5173）
make build          # 当前系统编译
make build-linux    # 交叉编译 Linux amd64
make build-frontend # 构建前端静态文件
make deploy[-backend|-frontend|-only]   # 部署（需要 .env 里的 DEPLOY_USER/DEPLOY_HOST）
make db-pull / db-import / logs / clean
```

**`make test` 已停用**（`Makefile` 里整段注释，help 文本是旧的）。改动验收按影响面选：

- 影响主链路（聊天 / 工具调用 / SSE / DB 写入）：`go build ./... && go test ./...` 全绿 + 新增针对性单测 + 真机跑一次聊天
- 只影响后台低危任务（记忆 / 摘要 / 主题 / 反思等）：`go build ./... && go test ./...` + 改前端则 `vite build` + 真机跑一次聊天并检查日志无 `[post_chat_group]` 等报错

## API（按模块，实现见对应文件）

| 模块 | 实现文件 | 内容 |
|------|----------|------|
| 认证 | `controller/router.go` | `/api/auth/*`（register/login/refresh/logout/change-password） |
| 对话 | `controller/chat.go` `conversations.go` `chat_stop.go` | `/api/chat`（SSE）、`/api/chat/stop`、`/api/chat/tool-result`、会话 CRUD / messages / messages-v2 / mode / plans / usage / restore / share-messages |
| 账单 / 笔记 / 记忆 / 摘要 | `bills.go` `notes.go` `memory.go` `summary.go` | 列表/统计/导出导入、批量增删、摘要按 id 取消息 |
| 智能体 / 工具 / Prompt | `agents.go` `tools.go` `prompts.go` | CRUD + `agents-for-input-bar` + prompt 版本回滚 |
| 文档 / 图片 / 提醒 | `documents.go` `images.go` `reminders.go` | 文档 CRUD + tags + 分享；图片上传/读取；提醒 ack/cancel |
| 信箱 / 主题 / Mission | `reflection_routes.go` | mailbox 读/归档/发起对话、themes、missions |
| 公开（无认证） | `router.go` | `/api/share/:token`、`/api/share/messages/:token`、`/images/:file`（仅 public） |
| Admin | `reflection_routes.go` `eval_routes.go` `cost.go` `llm_io_admin.go` `subagent_admin.go` | health、users、message debug、resource-lookup、replay、reflection traces、message-traces、`eval/*`、`cost/*`、`llm-io`、`subagents` |

## 开发约定

- 与用户交流只用中文（代码/注释/日志沿用项目既有风格）
- 新增功能先在 `docs/design/` 写计划文档，确认后开发
- model/ 只放结构体，DB 操作放 service/；工具实现放 tool/，不要耦合到 controller 或 agent
- 从数据库列表拉取数据时，必须做分页
- **禁止在循环内逐个访问数据库**（N+1）：插入用 `tx.Create(&slice)` 批量，更新/删除用带 `IN` 条件的 `Updates`/`Delete`，循环里只做内存操作
- 修改代码前必须先说明方案，征得用户同意；提交代码前必须征得用户同意
- 遇到相似功能，抽取公共组件
- **禁止对必要依赖做 nil 检查后静默返回**：通过依赖注入传入的必需组件（repo、client、worker 等）为 nil 意味着接线 bug，让它 panic，炸在明处。可选依赖（如回调）才用 nil 检查

## UI 预览与设计 token

讨论 UI 时**默认产出可交互 HTML 预览**，不要只给文字描述。

- 位置 `docs/design/<feature>/mockup.html`；单文件自包含（不引 CDN，离线可开）；配色只用下面的 token
- 必须带一个演示控制条（页面底部，挂 `.ui` 类以免干扰选区）：主题、字号，以及**我拿不准的取值做成可切换项**（如配色候选），让用户挑而不是我替他定
- 自查：改完先 headless 截图确认，再 `open` 给用户
  ```bash
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu \
    --hide-scrollbars --screenshot=/tmp/x.png --window-size=1340,940 \
    --virtual-time-budget=2200 "file://$PWD/docs/design/<feature>/mockup.html"
  ```
  要截交互态：把源码复制到 `/tmp` 并注入 `setTimeout` 脚本合成 `MouseEvent`/`click` 再截。事件必须派发到**具体元素**上——派发到 `document` 会让 `e.target.closest` 抛错

**设计 token**（唯一来源 `client/xHelper/xHelperMac/Assets.xcassets/*.colorset`）

| token | 浅 | 深 | | token | 浅 | 深 |
|---|---|---|---|---|---|---|
| appAccent | `#2F7D63` | `#92C8B4` | | chatBackground | `#F7EEDD` | `#1C1C1E` |
| accentSecondary | `#41C9E2` | 同（单色） | | chatBackgroundDeep | `#EFE2C8` | `#141416` |
| assistantText | `#3B2E1F` | `#FFFFFF` | | surfaceInput（=bubbleReply） | `#F2F7F3` | `#2C2E2D` |
| userText | `#2A7F58` | `#90D4A8` | | bubbleTool | `#E8E4EF` | `#2E2B36` |

- **不要自造色相**：淡色/描边一律 `color-mix(in srgb, assistantText N%, transparent)`，能落 token 就别写新 hex
- **表面语言**：窗口底 `.ultraThinMaterial`（`xHelperMac/App.swift:486`）；内容区/侧栏是 `.regularMaterial` 卡片（`CardBackground.swift`，r12，卡片间距 `cardGap=6`）；深色卡片靠**白阴影 + 白 0.5pt 描边**浮起，不是黑阴影
- 暖色渐变 + 光晕（`GlassBackground.swift`）**只有 ChatView / SettingsView 用**。文档页活在右侧栏卡片里（`RightSidebarView.swift:41` 的 `CardBackgroundModifier`），底色是**中性 material**，**不要拿暖米色 `chatBackground` 当页面底**（否则整页发黄）
- 正文基线：`FontSizeManager.baseSize=17`，正文 `scaledSize × 1.18`；字体栈 `-apple-system, "PingFang SC"`；列宽 728px
- **正文换 WKWebView 时必须全透明**（`isOpaque=false` + `backgroundColor=.clear` + HTML body 不铺不透明底色），否则糊掉卡片材质
- CSS 坑：带 `var()` 的派生变量在**声明它的元素**上求值 → 主题 class 必须挂 `html`（`:root`）；挂 `body` 会让 `:root { --bg: var(--chat-bg) }` 永远停在浅色

## 客户端（client/）约定

- **agent 不执行 `xcodebuild`**（避免与 Xcode 并发构建争 build database 锁），编译与运行验证由用户在 Xcode 完成
- 可用 `xcrun swiftc -typecheck` 自检，一个目标一批，**必须带项目真实设置**（`-default-isolation MainActor`、macOS 26.3 / iOS 26.0），漏了会假绿：
  ```bash
  cd client/xHelper/xHelper/Shared
  xcrun swiftc -typecheck -sdk "$(xcrun --show-sdk-path --sdk macosx)" \
    -target arm64-apple-macos26.3 -default-isolation MainActor \
    -I <DerivedData>/Build/Products/Debug $(cat /tmp/shared.txt) ../../xHelperMac/*.swift
  # iOS：sdk 换 iphonesimulator，target 换 arm64-apple-ios26.0-simulator，
  #      -I 用 <DerivedData>/Index.noindex/Build/Products/Debug-iphonesimulator
  ```
  `/tmp/shared.txt` 是 `Shared/` 的文件清单（新增文件补一行）；Mac 源集 = `Shared/**` + `xHelperMac/*.swift`
- **两个 `@main` 入口各写各的**：`xHelper/App.swift`（iOS）、`xHelperMac/App.swift`（macOS，实际在用）。`swiftc` 一批最多含一个 `@main` → **入口文件天生在 typecheck 盲区**。在入口挂的全局 modifier（`.toast()` / `.lightbox()` 等）**两个入口都要挂**，改完用 grep 确认，否则症状是"改了但完全没生效"
- **声明 `ObservableObject` / `@Published` 的文件必须显式 `import Combine`**（只 `import SwiftUI` 在 Xcode 里不够，且会让引用它的其它文件连带报 `Cannot find 'X' in scope`，极易误判成 target 成员问题）。`swiftc -typecheck` **复现不了这条**，必须真机重编才算验过
- `Message` / `Conversation` 等模型：可变字段用 `var`（`let` 带默认值不会被合成的 `Codable` 解码）；**不要用 memberwise init 重建已有消息**（静默丢新字段）
- **macOS 设置页子页面风格统一**：卡片分组用 `SettingsCard` + `KeycapView`（`client/xHelper/xHelperMac/SettingsStyle.swift`），规范见 `docs/design/mac-apps/settings-sheet-style.md`
- 纯逻辑文件在 `client/xHelper/Package.swift` 里镜像成 SwiftPM target，可用 `swift test` 跑（不碰 Xcode）
- **阅读器（文档正文 WKWebView）有两个自查脚本**，模板都从 Swift 源码现抠（`scripts/reader-template.py`，不会漂移），改了模板必须两个都跑：
  - `scripts/check-reader-html.sh` —— headless Chrome 里跑模板/JS（渲染、查找、目录跳转、`::selection` 规则），快。**测不了选区的绘制**：Blink 与 WebKit 差别很大（WebKit 会把跨段整块选中画成横跨视图的大矩形，Chrome 不画），拿它判选区长短会得出错误结论。
  - `scripts/snapshot-reader.sh [输出目录] [light|dark]` —— 真引擎 WKWebView 离屏加载 + 合成选区/折叠/画划线（`marks` job）+ 截图 + 探针（选区长、`hasSel`、每个被覆盖文本节点有几个矩形）。**选区绘制、真实渲染、划线配色这类问题只看这个**。外观必须显式传（默认 light，跟随系统会截出说不清的图）
- **画划线只能逐文本节点包**（`splitText` + 各自 `insertBefore(<mark>)`），**不许 `range.extractContents()` + `insertNode()` 搬 DOM**：跨块时前者会给被部分包含的块级元素造克隆插到 range 起点（起点在 `<h2>` 里＝标题里套列表；表格行更直接被塞进 `<mark>`），非法嵌套下 WebKit 会少画字或重组整段行（真机丢过字），且那坨克隆不是 mark、`unwrapAll()` 擦不掉。自查脚本里「画线不改正文文本 / 不搬块结构 / mark 文本 == 命中区间」这几条就是钉它的
- **划线是「文字底部波浪线」不是底色高亮**：`mark.hl` 用原生 `text-decoration: underline wavy` + 线色/线粗/`text-underline-offset`（引擎逐行片段画，跨段/列表/表格天然正确），并且**必须写 `background: transparent`** —— `mark` 的 UA 默认底色是黄的，去掉原来那行底色后会从波浪线下面透出来。线色要实（浅色赭金 `rgba(217,151,27,1)`，暖米底上的浅琥珀实测"太浅不显眼"），半透明的线在暖米底上看不见。重叠两条线不错开（只靠 hover 的「重叠划线」标签提示）；`flash` 动画要动 `text-decoration-color`（不再是 `background`）
- **选区不要自绘**（踩过两次，见 `docs/design/document-highlight-annotation/design.md` 的 P2 段）：用 `::selection { color: <写死的 token 值>; background-color: transparent }` 交给引擎画。自绘（`::highlight(sel)` + `selectionchange`）修不掉两件事 —— ①「自己带文本 + 后跟子列表」的容器 `li`，它的直接文本节点永远不着色（range 覆盖了、`getClientRects()` 有矩形，WebKit 就是不画）；② JS 查不到「选区此刻是否可见」，引擎标成不可见但 `getSelection()` 仍返回非折叠 range 时，色带会赖着不走。`var()` 在 `::selection` 里解析不出来，颜色只能写死

## 后端错误处理规范

- **所有 error 必须打日志**，严禁 500 或其他错误状态下不记录原因
- **一个 error 只打一次日志**，禁止同一 error 在多层重复打印
- 不打断流程的 error（可降级继续）：就地 `log.Printf` 后继续执行
- 需向上抛出的 error：`fmt.Errorf("context: %w", err)` 附加信息后返回，在调用链最顶层 `log.Printf` 一次
- 所有 `view.JSONError(c, 500, ...)` 前必须有 `log.Printf` 记录原始 error

## 文档地图

- `docs/design/` — 设计文档主战场，`phase-1` ~ `phase-18` 按阶段归档；`mac-apps/` 是 macOS 客户端专项；每个设计目录下可放 `mockup.html` 交互预览（规范见「UI 预览与设计 token」）
- 常读：`progressive-tool-disclosure.md`、`post-chat-worker-group.md`、`plan-mode-readonly.md`、`phase-18-subagent-explore.md`、`retrieval` 相关、`mac-apps/settings-sheet-style.md`、`mac-apps/swift6-concurrency-fix.md`
- `docs/bugs/` — 按编号记录（到 014）；`docs/front-end/plan.md` — 前端架构与 nginx 部署
- `docs/learning/` `docs/research/` `docs/security/` `docs/todo/` — 学习笔记 / 调研 / 红队测试 / 待办

## 已知过时/废弃（不要照着做）

- `server/Makefile` 的 `build-topic-fix` / `build-backfill` / `build-rebuild` 指向的 `cmd/` 子目录已不存在
- `tool.DynamicToolLoader` 接口是死代码（无实现无调用）；"每 30s 从 DB 加载动态工具 + sidecar 转发"不成立。MCP proxy 工具已下掉，`mcp.Manager` 仅剩监控统计用途
- `docs/agents/*.md`、`docs/backend-reference.md` 是历史脚手架（引用的 `CONTEXT-MAP.md`、`docs/adr/`、`/api/devices/register` 都不存在）
- 根 `scripts/*.sh` 是 device-token 时代脚本，已不适用；服务端脚本在 `server/scripts/`
