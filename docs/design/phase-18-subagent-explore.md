# Phase 18：SubAgent 系统设计方案（分阶段实施）

## 零、背景与目标

### 0.1 问题

xhanHelper 是单会话单 ReAct 循环架构。浏览器能力接入后，一次联网调研会：

1. 抓取多页网页原文 → 主会话上下文膨胀 50-100KB
2. 每次新网页内容不同 → DeepSeek 上下文前缀缓存在网页注入点之后**全部 miss**（虽然总体命中率 97%+，但网页调研场景里网页位置之后的 token 每次都是新的）
3. 调研结束后网页 token 仍跟随后续对话

### 0.2 方案

借鉴 [opencode subAgent](https://github.com/anthropics/claude-code/blob/main/packages/opencode/src/tool/task.ts) 机制：**子任务在独立的子会话（Conversation）中运行，网页原文只进子上下文，主会话只收到结构化结论**。

### 0.3 opencode 对应关系

| opencode | xhanHelper |
|----------|------------|
| `Agent.Info{name, mode:"subagent", prompt, permission}` | Conversation.Extra.SubAgentType + 角色 prompt |
| `tool/task.ts` （task 工具） | `tool/task.go` |
| `sessions.create({parentID})` | `Conversation.ParentID`（字段已存在，conversation.go:25） |
| `Permission.merge` + 子 deny 规则 | 工具白名单双层拦截（工具定义过滤 + MiniReAct `AllowedTools` 运行时 guard） |
| `renderOutput`（`<task id state>` XML） | 同格式 |
| `BackgroundJob` + `inject` 后台通知 | Phase 19（本期不做） |
| `subagent_depth` 配置 | 写死 1 层 |

### 0.4 分阶段总览

```
Phase 18-1 (webfetch + Bocha 下掉)
     │
     ├──→ Phase 18-2 (SubAgent 基础设施：模型/接口/类型)
     │         │
     │         └──→ Phase 18-3 (explore 子 agent：复用 MiniReAct，同步阻塞)
     │                   │
     │                   └──→ Phase 18-4 (端到端验收 + 收尾)
     │
     └── (Phase 18-2 可并行 review，但推荐串行实施)
```

---

## 一、Phase 18-1：webfetch 工具 + Bocha 搜索下掉

**目标**：subAgent（explore）获得服务端直抓 URL 能力——webfetch 仅子 agent 可见，对主循环隐藏（见 1.2）；下掉已停止付费的 Bocha 搜索。

**依赖**：无。

**新增文件**：

| 文件 | 说明 |
|------|------|
| `server/internal/tool/webfetch.go` | webfetch 工具实现 |
| `server/internal/tool/webfetch_test.go` | 单元测试 |

**修改文件**：

| 文件 | 改动 |
|------|------|
| `server/internal/appinit/tools.go` | 注册 webfetch 并 `HideTool("webfetch")`；移除 Bocha 搜索注册 |
| `server/internal/tool/registry.go` | `SearchTools` 跳过 hidden 工具（堵 tool_retriever.search 泄漏，见 1.2） |
| `server/conf/config.yaml` | 加 `webfetch:` 配置节 |

### 1.1 webfetch 工具定义

```go
// tool/webfetch.go
package tool

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
    "unicode"

    "golang.org/x/net/html"
)

type WebFetchTool struct {
    client   *http.Client
    maxBytes int
}

func NewWebFetchTool(timeoutSecs int, maxBytes int) *WebFetchTool {
    if timeoutSecs <= 0 {
        timeoutSecs = 15
    }
    if maxBytes <= 0 {
        maxBytes = 51200
    }
    return &WebFetchTool{
        client: &http.Client{
            Timeout: time.Duration(timeoutSecs) * time.Second,
        },
        maxBytes: maxBytes,
    }
}

func (t *WebFetchTool) Name() string       { return "webfetch" }
func (t *WebFetchTool) Description() string {
    return "抓取指定 URL 的网页正文。返回纯文本内容（HTML 已剥离标签）。适合快速获取页面信息。不适用于需要 JS 渲染的动态页面——此类页面请用 browser 工具。"
}
func (t *WebFetchTool) ReadOnlyFuncs() map[string]bool { return AllReadOnly(t) }

func (t *WebFetchTool) Functions() []FuncSchema {
    return []FuncSchema{{
        Name:        "fetch_url",
        Description: "抓取指定 URL 的网页内容，返回剥离 HTML 标签后的纯文本",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "url": map[string]interface{}{
                    "type":        "string",
                    "description": "要抓取的网页 URL（完整的 https?:// 地址）",
                },
            },
            "required": []interface{}{"url"},
        },
    }}
}

func (t *WebFetchTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
    switch funcName {
    case "fetch_url":
        return t.fetchURL(ec.Ctx, args)
    default:
        return &Result{Success: false, Error: fmt.Sprintf("未知函数: %s", funcName)}
    }
}
```

### 1.2 可见性：webfetch 仅 subAgent 可用

**需求**：webfetch 不暴露给主 agent（主循环 LLM 不得看到/调用），只允许子 agent（explore）使用。

**实现**：`webfetch.enabled=true` 时注册，随后调用 `reg.HideTool("webfetch")`（与 tool_retriever 同款模式，registry.go:64）；默认关闭。隐藏的生效面：

| 路径 | 行为 |
|------|------|
| 主循环原生 FC defs（`GetNativeFCTools`） | 不含 webfetch（sortedVisibleToolNames 排除） |
| 主循环 prompt-eng prompt（`GeneratePromptWithOverview`，promptbuilder/static.go:51） | 不含 webfetch |
| `tool_retriever.search`（Registry 内存搜索，registry.go:69） | **需修复**：`SearchTools` 当前不过滤 hiddenNames，主 LLM 可直接 search 出 webfetch 完整定义 → 加 `if r.hiddenNames[name] { continue }`（顺带堵 ExecuteByName 模糊纠错建议，registry.go:559） |
| `tool_retriever.search`（DB keyword/vector 策略） | 无泄漏：webfetch **不进 `builtinToolSeeds`**（seeder.go:34）。DB seed 功能后续整体下掉，不做 DB 侧过滤 |
| `ExecuteByName` | 仍可执行（子循环经 MiniReAct 白名单调用） |
| 反思系统（reflectionReg）/ profile_worker | 不涉及：reflectionReg 是独立 registry；profile_worker 用显式名单 |

**不做**（防御纵深）：`ExecuteByName` 对 hidden 工具的执行拦截。主循环 LLM 无任何途径获知 webfetch 存在（defs/prompt/检索均不可见），幻觉调用概率等价于幻觉不存在的工具名，现有未知工具纠错已能拒绝。保持 HideTool 语义简单（"对 LLM 隐藏，但可执行"）。

### 1.3 fetchURL 实现

流程：

1. 解析 URL 参数
2. 创建 GET 请求，设置 `User-Agent: "Mozilla/5.0 (compatible; xhanHelper/1.0)"`
3. 若 HTTP 错误（404/50x）→ 返回错误带状态码
4. 若 `Content-Type` 非 `text/html` 或 `text/plain` → 返回 `"内容类型为 <type>，请改用 browser 工具访问该页面"`
5. 读 body，`io.LimitReader` 限制 `t.maxBytes`
6. 根据 Content-Type：
   - `text/html` → html2text（见 1.4）
   - `text/plain` → 直接返回
7. 截断到 `t.maxBytes`（如果 html2text 后仍超限，尾部加 `"[内容已截断]"`）
8. 返回 JSON: `{"url": "...", "title": "<title 内容或空>", "text": "<正文>"}`

### 1.4 html2text 提取

```go
// 用 golang.org/x/net/html Tokenizer 抽取正文文本
func html2text(body []byte) string {
    z := html.NewTokenizer(bytes.NewReader(body))
    var buf strings.Builder
    skipDepth := 0 // <script>/<style>/<noscript> 内跳过
    var last rune
    for {
        tt := z.Next()
        switch tt {
        case html.ErrorToken:
            return strings.TrimSpace(buf.String())
        case html.StartTagToken, html.SelfClosingTagToken:
            raw, _ := z.TagName()
            tag := string(bytes.ToLower(raw))
            if tag == "script" || tag == "style" || tag == "noscript" {
                skipDepth++
            }
            if tag == "br" || tag == "p" || tag == "div" || tag == "li" ||
                tag == "h1" || tag == "h2" || tag == "h3" || tag == "h4" {
                buf.WriteRune('\n')
            }
        case html.EndTagToken:
            raw, _ := z.TagName()
            tag := string(bytes.ToLower(raw))
            if tag == "script" || tag == "style" || tag == "noscript" {
                if skipDepth > 0 {
                    skipDepth--
                }
            }
        case html.TextToken:
            if skipDepth == 0 {
                text := strings.TrimSpace(string(z.Text()))
                if text != "" {
                    if last != 0 && !unicode.IsSpace(last) {
                        buf.WriteRune(' ')
                    }
                    buf.WriteString(text)
                    last = rune(text[len(text)-1])
                }
            }
        }
    }
}
```

### 1.5 配置

`conf/config.yaml` 新增：

```yaml
webfetch:
  enabled: false          # 默认关闭（token 消耗大）；启用后仅 subAgent 可见
  timeout_seconds: 15
  max_bytes: 51200
```

`config/config.go` 增加对应结构体字段，从 YAML 加载。

### 1.6 Bocha 搜索移除

`appinit/tools.go`：

```go
// 删除以下代码块：
if cfg.Search.Enabled {
    reg.Register(tool.NewLoggingWrapper(
        tool.NewBochaSearchTool(cfg.Search, repos.SearchUsage), log.Default()))
} else {
    reg.Register(tool.NewLoggingWrapper(
        tool.NewSearchTool(cfg.Search, repos.SearchUsage), log.Default()))
}
```

同时检查 `config.SearchConfig` / `config.Search` 字段是否仅在搜索工具中使用——若是，一并移除结构体定义和 YAML 加载。`SearchUsageRepo` 注入点也移除。**保留 `search_usage` 表结构和模型不删**（历史数据不动）。

### 1.7 测试（`tool/webfetch_test.go` + `tool/registry_hidden_test.go`）

| 用例 | 输入 | 预期 |
|------|------|------|
| 正常 HTML | 含 `<title>`、`<p>`、`<script>`、`<style>` 的 HTML | 返回纯文本，不含 script/style 内容，含 title |
| 纯文本 | `Content-Type: text/plain` | 直通返回 |
| 非 HTML 类型 | `Content-Type: application/json` | 错误 "请改用 browser 工具" |
| 404 | HTTP 404 | 错误含状态码 |
| 超时 | 无响应的服务器 | 超时错误 |
| 超限截断 | body > maxBytes | 截断末尾加 "[内容已截断]" |
| 空白/空页面 | 空白 HTML | 返回空 text |
| 隐藏后主循环不可见 | `HideTool("webfetch")` 后 `GetNativeFCTools` / `GeneratePromptWithOverview` | 不含 webfetch |
| `SearchTools` 跳过 hidden | 注册 hidden 工具后 `SearchTools` | 结果不含 hidden 工具 |
| `GetLLMToolsFiltered` 含 hidden | 注册 hidden 工具后 `GetLLMToolsFiltered(["webfetch"])` | 按显式名单可导出（子 agent 路径依赖） |

hidden 语义测试在 `registry_hidden_test.go`（复用 mockOrderTool，不依赖真实 webfetch 工具）。

### 1.8 Phase 18-1 验收

- `go test ./tool/ -run WebFetch -v` 绿（含 1.7 新增 hidden 用例）
- webfetch 已隐藏：主循环 LLM 的 tools/prompt 中不含 webfetch，`tool_retriever.search("webfetch")` 查不到
- 真机 webfetch 功能验证推迟到 Phase 18-3（子 agent 就绪后走调研场景；主循环无法直接调用隐藏工具）

---

## 二、Phase 18-2：SubAgent 基础设施（模型/接口/类型）

**目标**：所有新类型、接口、骨架代码就绪，`go build ./...` 通过，运行时不改变任何现有行为（task 工具不注册）。

**依赖**：Phase 18-1（webfetch 已存在于 registry，Phase 18-2 不依赖它但并行开发友好）。

**新增文件**：

| 文件 | 说明 |
|------|------|
| `server/internal/tool/subagent.go` | `SubAgentRunner` 接口 + `SubAgentRunOptions` + `SubAgentRunResult` + `ClientToolInfo` |
| `server/internal/tool/task.go` | task 工具（完整实现：包裹/部分结果逻辑，不注册） |

**修改文件**：

| 文件 | 改动 |
|------|------|
| `server/internal/model/conversation.go` | `ConversationExtra` 加 `SubAgentType` |
| `server/internal/tool/tool.go` | `ExecuteContext` 加 `ClientTools []ClientToolInfo` |
| `server/internal/service/conversation_repo.go` | `ListByUser` 加 `parent_id IS NULL` |
| `server/internal/agent/orchestrator.go` | `executeConcurrently` 构造 `ec` 时填入 `ClientTools`（从 `req.ClientTools` 转换） |

### 2.1 `model/conversation.go` — Extra 扩展

```go
// ConversationExtra — Conversation.Extra 的结构化数据
type ConversationExtra struct {
    UsedToolIDs  []string         `json:"used_tool_ids"`
    Context      *ConversationCtx `json:"context,omitempty"`
    SubAgentType string           `json:"subagent_type,omitempty"` // 新增：子会话类型（"explore"等），空=普通会话
}
```

`Conversation.ParentID` 字段已存在（conversation.go:25，注释"子会话（subAgent）的父会话 ID"），无需迁移。

**判断子会话的唯一依据**：`GetExtra().SubAgentType != ""`。ParentID 仅用于深度检查和父链追溯。

### 2.2 `tool/subagent.go` — 接口定义（新文件）

```go
package tool

import (
    "context"

    "xhan-helper/internal/llm"
)

// ClientToolInfo — 客户端工具信息的最小化结构（tool 包不能 import agent 包，故独立定义）
type ClientToolInfo struct {
    ServerName  string                 `json:"server_name"`
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    Parameters  map[string]interface{} `json:"parameters"`
    IsReadOnly  bool                   `json:"is_read_only"`
}

// SubAgentRunOptions — task 工具向 runner 传递的参数
type SubAgentRunOptions struct {
    SubAgentType string            // 子 agent 类型："explore"
    Description  string            // 3-5 词任务描述
    Prompt       string            // 任务全文
    ParentConvID string            // 主会话 ID
    ClientTools  []ClientToolInfo  // 透传客户端工具定义（explore 要用浏览器）
    MainEvents   chan<- *llm.StreamEvent // 主 SSE events 通道（子循环需转发 client_tool_call 给客户端）
}

// SubAgentRunResult — runner 返回的结果
type SubAgentRunResult struct {
    Success bool
    Error   string
    Text    string // 子 agent 最终回复（未包裹 <task> 标签，由 task 工具包裹）。
    // 超时/失败时 Text 为工具日志摘要（部分成果），task 工具以 state=timeout/failed 包裹回传主 LLM
    ConvID string // 子会话 ID
    State  string // 执行状态：completed | timeout | failed。空默认 completed
}

// SubAgentRunner — 子 agent 执行器接口
// 由 agent 包 Orchestrator 实现，解决 tool → agent 循环依赖。
type SubAgentRunner interface {
    RunSubAgent(ctx context.Context, opts *SubAgentRunOptions) *SubAgentRunResult
}
```

### 2.3 `tool/tool.go` — ExecuteContext 扩展

在 `ExecuteContext` struct 内新增字段：

```go
type ExecuteContext struct {
    Ctx    context.Context
    UserID string
    ConvID string
    Mode   string

    // Events — SSE 事件通道，工具可以通过此通道向客户端推送自定义事件。
    // 重要：必须在工具 Execute() 方法返回前发送，不可在异步 goroutine 中使用 channel。
    // channel 在 ReAct 循环结束后关闭，延迟发送会 Panic。replay/测试场景下为 nil。
    Events chan<- *llm.StreamEvent

    // ClientTools — 客户端上报的本地工具定义（task 工具用此字段传给子循环）。
    // 普通工具无需关注，仅 task 工具使用。replay/测试场景下为 nil 或空。
    ClientTools []ClientToolInfo
}
```

### 2.4 `tool/task.go` — 完整实现（新文件）

```go
package tool

import (
    "encoding/json"
    "fmt"
    "strings"
)

type TaskTool struct {
    runner SubAgentRunner // Phase 18-2 为 nil，Phase 18-3 注入
}

func NewTaskTool(runner SubAgentRunner) *TaskTool {
    return &TaskTool{runner: runner}
}

func (t *TaskTool) Name() string                     { return "task" }
func (t *TaskTool) ReadOnlyFuncs() map[string]bool   { return AllReadOnly(t) }

func (t *TaskTool) Description() string {
    return `启动一个子 agent 独立完成单一目标的小粒度任务，执行完毕后返回结果。

核心原则：一个 task 只做一件事。任务应在子 agent 有限的轮次预算内完成；大任务拆成多个 task 并行，不要塞进一个 task。

何时使用：
- 需要联网调研多步信息（搜索 → 浏览网页 → 提炼结论）
- 多个独立子任务可并行（利用并行加速）
- 任务量大、需要独立上下文避免污染当前对话

何时不用：
- 单次简单的查询/计算/查笔记 → 直接用对应工具
- 目标不明确、方向需用户中途决策 → 先与用户澄清，不要贸然派 task
- 任务太大（预计超出子 agent 轮次预算）→ 拆成多个 task

用法：
- 子任务的内部思考与最终回复不展示给用户（用户仅看到工具调用过程），你必须用自然语言总结转发给用户
- prompt 要写详细：单一目标、明确要调研什么、返回什么格式`
}

func (t *TaskTool) Functions() []FuncSchema {
    return []FuncSchema{{
        Name:        "run",
        Description: "启动子 agent 执行任务。子 agent 在独立上下文中运行，完成后返回结果。可并发执行多个 task。",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "description": map[string]interface{}{
                    "type":        "string",
                    "description": "任务简短描述（3-5 词，用作子任务标题，如 调研XX技术）",
                },
                "subagent_type": map[string]interface{}{
                    "type":        "string",
                    "description": "子 agent 类型，当前可用：explore（联网调研，只读）",
                },
                "prompt": map[string]interface{}{
                    "type":        "string",
                    "description": `给子 agent 的完整任务描述。必须自包含（子 agent 不共享当前对话上下文）。

任务必须单一目标、小粒度（一次信息收集循环能完成）；大任务拆成多个 task 并行。

拆分示例（用户请求 → 多个子 task）：
- "调研 Vue/React/Svelte 哪个适合我" → 拆 3 个 task："调研 Vue 3.5 特性与适用场景"、"调研 React 19 特性与适用场景"、"调研 Svelte 5 特性与适用场景"，全部完成后由你综合对比给出建议
- "调研 AI 行业动态" → 目标太宽泛，先与用户确认具体方向（如大模型/Agent/具身智能），再按主题拆 task
- "调研 DeepSeek 上下文缓存机制" → 单一主题，一个 task 搞定：机制原理、计费方式、适用场景
- "帮我搞定毕业论文" → 粒度太大且需用户中途决策，应分步进行，不要一次派 task`,
                },
            },
            "required": []interface{}{"description", "subagent_type", "prompt"},
        },
    }}
}

func (t *TaskTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
    if t.runner == nil {
        return &Result{Success: false, Error: "task 工具尚未启用（runner 未注入）"}
    }
    var params struct {
        Description  string `json:"description"`
        SubAgentType string `json:"subagent_type"`
        Prompt       string `json:"prompt"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return &Result{Success: false, Error: fmt.Sprintf("参数解析失败: %v", err)}
    }

    opts := &SubAgentRunOptions{
        SubAgentType: params.SubAgentType,
        Description:  params.Description,
        Prompt:       params.Prompt,
        ParentConvID: ec.ConvID,
        ClientTools:  ec.ClientTools,
        MainEvents:   ec.Events,
    }
    result := t.runner.RunSubAgent(ec.Ctx, opts)

    // 失败且无任何产出 → 硬错误（主 LLM 需知道子任务不可用）
    if !result.Success && strings.TrimSpace(result.Text) == "" {
        return &Result{Success: false, Error: result.Error}
    }

    // 正常完成，或超时/失败但有部分产出（Text 为工具日志摘要）→ 包裹返回，
    // 让主 LLM 利用部分成果。state 由 runner 标注实际状态。
    state := result.State
    if state == "" {
        state = "completed"
    }
    summary := fmt.Sprintf("子任务完成: %s", params.Description)
    if !result.Success {
        summary = fmt.Sprintf("子任务%s（含部分结果）: %s", state, params.Description)
    }

    // 包裹结果（opencode renderOutput 风格）
    output := fmt.Sprintf(
        `<task id="%s" state="%s">
<summary>%s</summary>
<task_result>
%s
</task_result>
</task>`,
        result.ConvID, state, summary, result.Text,
    )
    data, _ := json.Marshal(map[string]string{"result": output})
    return &Result{Success: true, Data: data}
}
```

### 2.5 `service/conversation_repo.go` — 列表过滤

`ListByUser` 方法 WHERE 子句加 `AND parent_id IS NULL`：

```go
// 前：
q := r.db.WithContext(ctx).Where("user_id = ? AND is_deleted = ?", userID, false)

// 后：
q := r.db.WithContext(ctx).Where("user_id = ? AND is_deleted = ? AND parent_id IS NULL", userID, false)
```

影响面：前端会话列表 API（`controller/conversations.go:124`），无其他调用处。

### 2.6 `agent/orchestrator.go` — 一处修改

> 原方案的 `RunRequest.ToolWhitelist` + `executeConcurrently` 白名单参数 + `mkLlmChatReq` 过滤已**全部取消**：
> 白名单在 MiniReAct 内生效（工具定义过滤 + `AllowedTools` 运行时 guard），主循环代码无需任何白名单逻辑。

#### 2.6.1 EC 构造注入 ClientTools

```go
// 前（line 1375）：
ec := &tool.ExecuteContext{Ctx: ctx, UserID: userID, ConvID: convID, Mode: mode, Events: events}

// 后：
ec := &tool.ExecuteContext{
    Ctx:         ctx,
    UserID:      userID,
    ConvID:      convID,
    Mode:        mode,
    Events:      events,
    ClientTools: clientTools,
}
```

`clientTools` 由 `req.ClientTools`（`[]agent.ClientTool`）转换为 `[]tool.ClientToolInfo`，在 `finalizeRound` 调用 `executeConcurrently` 处构造一次。

### 2.7 Phase 18-2 验收

- `go build ./...` 编译通过
- `go test ./...` 无回归
- 会话列表 API 返回正常（前端看不到子会话——即使测试库有 ParentID 非 nil 的记录）

---

## 三、Phase 18-3：explore 子 agent 完整实现（复用 MiniReAct）

**目标**：主循环可调 task 工具 → explore 在独立上下文中完成联网调研 → `<task>` 结果返回主循环。

**核心决策**：同步阻塞执行（task 工具阻塞主循环，`explore_timeout_seconds` 上限 + 进度事件转发 + 超时部分结果回传）。不新增第三套循环——扩展既有 MiniReAct（其注释即声明面向 subAgent 场景，且已具备强制收敛轮/工具日志/事件回调）。

**依赖**：Phase 18-1（webfetch 必须在 registry 中）+ Phase 18-2（接口/类型/EC.ClientTools 就绪）。

**新增文件**：

| 文件 | 说明 |
|------|------|
| `server/internal/agent/subagent.go` | explore 角色定义 + 白名单 + `RunSubAgent` + `buildSubAgentSystemPrompt` + 事件 forwarder + 超时摘要 |
| `server/internal/agent/subagent_test.go` | 深度检查、结果包裹、事件转发、超时部分结果、MsgSink 落库测试 |

**修改文件**：

| 文件 | 改动 |
|------|------|
| `server/internal/agent/client_tools.go` | 提取 `clientToolCaller` 共享客户端工具执行器（orchestrator 改用它，行为零变化） |
| `server/internal/agent/mini_react.go` | 扩展：ClientTools/ClientBridge/ClientToolTimeout、AllowedTools、ConvID、MsgSink、客户端工具 fast-fail |
| `server/internal/appinit/agents.go` | Orchestrator 注入 SubAgent 配置；构造后 `registry.Register(taskTool)` |
| `server/conf/config.yaml` | 加 `subagent:` 配置节 |

### 3.1 explore 角色 prompt（`agent/subagent.go`）

```go
const explorePrompt = `你是联网调研专家，擅长通过网页抓取与本地检索收集信息并提炼结论。

能力：
- 用 webfetch 抓取具体 URL 的正文（注意：结果全部进入上下文，token 消耗大——单页可达 5 万字符。只抓最有价值的一两个页面，优先汇总页/官方文档/百科，禁止逐页深挖）
- 用 browser 工具访问需要 JS 渲染的动态页面（需 Mac 客户端在线）
- 用 unified_search、search_memories_v2、search_notes_v2、message_search 结合用户笔记/记忆进行本地调研
- 仅做只读操作，禁止写入任何数据

工作准则：
- 先规划：明确调研目标、需要什么信息、从哪里获取
- 信息收集优先用高效来源（汇总页、官方文档、百科），避免逐页深挖
- 判断信息可信度：优先官方来源，标注不确定处
- 不要中途汇报进度，一次性完成全部调研后输出结论

最终输出必须严格使用以下结构（不要在结构外输出任何文字）：
<research_result>
<summary>3-5 句话的总体结论</summary>
<points>要点列表，每条不超过 50 字，用分号分隔</points>
<sources>来源清单：标题 | URL | 置信度(高/中/低)</sources>
<open_questions>未解决的问题（没有则填"无"）</open_questions>
</research_result>`

// exploreWhitelist — explore 子 agent 可用工具白名单
// 数据边界：仅保留笔记检索（search_notes_v2）；记忆/历史/摘要（unified_search、
// search_memories_v2、message_search）对子任务不可见——子任务不应自动读取私密对话数据
var exploreWhitelist = []string{
    "webfetch",
    "browser",
    "ui", // 用户介入（ask_user），仅客户端在线时注入 defs
    "search_notes_v2",
    "datetime",
    "calculator",
}
```

### 3.2 `Orchestrator.RunSubAgent` 实现（SubAgentRunner 接口）

```go
func (o *Orchestrator) RunSubAgent(
    ctx context.Context,
    opts *tool.SubAgentRunOptions,
) *tool.SubAgentRunResult {
```

**执行流程**：

#### 步骤 1 — 深度检查

```go
// 写死 ≤1 层：主会话可派子会话，子会话不可再派
depth := 0
currentID := opts.ParentConvID
for {
    parent, err := o.convRepo.FindByID(ctx, currentID)
    if err != nil || parent == nil || parent.ParentID == nil {
        break
    }
    depth++
    currentID = *parent.ParentID
}
if depth >= 1 {
    return &tool.SubAgentRunResult{
        Success: false,
        Error:   "已达到子任务深度限制（1层）。当前会话已是子任务，不可再派生于任务。请直接完成该调研。",
    }
}
```

#### 步骤 2 — 获取主会话 UserID

```go
mainConv, err := o.convRepo.FindByID(ctx, opts.ParentConvID)
if err != nil || mainConv == nil {
    return &tool.SubAgentRunResult{Success: false, Error: "无法获取父会话信息"}
}
userID := mainConv.UserID
```

#### 步骤 3 — 创建子 Conversation

```go
extraJSON, _ := json.Marshal(&model.ConversationExtra{SubAgentType: opts.SubAgentType})
subConv := &model.Conversation{
    ID:       idgen.Next(),
    UserID:   userID,
    Title:    fmt.Sprintf("%s (%s)", opts.Description, opts.SubAgentType),
    ParentID: &opts.ParentConvID,
    Extra:    string(extraJSON),
    Mode:     model.ConvModeWork,
}
if err := o.convRepo.Create(ctx, subConv); err != nil {
    return &tool.SubAgentRunResult{Success: false, Error: fmt.Sprintf("创建子会话失败: %v", err)}
}
```

#### 步骤 4 — 转换 ClientTools

```go
agentClientTools := make([]ClientTool, len(opts.ClientTools))
for i, ct := range opts.ClientTools {
    agentClientTools[i] = ClientTool{
        ServerName:  ct.ServerName,
        Name:        ct.Name,
        Description: ct.Description,
        Parameters:  ct.Parameters,
        IsReadOnly:  ct.IsReadOnly,
    }
}
```

#### 步骤 5 — 构建工具定义（白名单过滤，双重来源）

```go
// 服务端工具：GetLLMToolsFiltered 按显式名单导出（不跳过 hidden——webfetch 对主循环隐藏但子 agent 可用）
// 客户端工具：buildClientToolDefs 按 server_name 分组后按白名单过滤
tools := o.registry.GetLLMToolsFiltered(exploreWhitelist)
tools = append(tools, filterClientToolsByWhitelist(buildClientToolDefs(agentClientTools, o.registry), exploreWhitelist)...)
```

`filterClientToolsByWhitelist` 按 `ToolDefinition.Name`（= server_name）过滤——`browser` 等客户端 server 在 whitelist 内才下发。`GetLLMToolsFiltered` 遍历白名单名从注册表导出，未注册名（客户端 server，如 `browser`）自动跳过；白名单为编译期固定顺序，输出字节稳定（前缀缓存友好）。两个 helper（`filterClientToolsByWhitelist` / `whitelistMap`）为 `subagent.go` 内的小函数。

#### 步骤 6 — 构造 MiniReAct（复用既有组件）

```go
maxIter := o.subAgentMaxIterations
if maxIter <= 0 {
    maxIter = 20 // 兜底防死循环（timeout 才是真实预算）
}
react := NewMiniReAct(&MiniReActConfig{
    Client:            o.flashClient, // 子 agent 固定 Flash
    Tools:             tools,
    ToolRegistry:      o.registry,
    SystemPrompt:      buildSubAgentSystemPrompt(req),
    UserID:            userID,
    ConvID:            subConv.ID,
    MaxIterations:     maxIter, // 主循环 maxIter-1 轮 + 1 轮强制收敛（MiniReAct 内置，见 3.3）
    Caller:            "subagent:" + opts.SubAgentType,
    ClientTools:       agentClientTools,
    ClientBridge:      o.clientBridge,
    ClientToolTimeout: o.clientToolTimeout,
    AllowedTools:      whitelistMap(exploreWhitelist),
    MsgSink:           newSubAgentMsgSink(o.msgRepo, subConv.ID, userID),
    OnEvent:           makeSubAgentForwarder(opts.MainEvents),
})
```

#### 步骤 7 — 事件 forwarder（进度反馈，复用主 SSE 事件类型）

子循环执行期间主循环阻塞于 `wg.Wait()`，主 SSE 无其他事件——**复用现有事件类型**，前端零改动：

| 子循环事件 | 转发动作 |
|-----------|---------|
| `client_tool_call` | **阻塞送达**（5s 超时兜底，对齐 executeClientTool 语义）——浏览器指令必须到客户端 |
| `status`（客户端工具执行期间心跳） | best-effort，加"子任务"前缀 |
| `tool_call` / `tool_result`（客户端工具执行器展示事件） | best-effort 原样转发 |
| `mini_react:status` | 转 `status`，加"子任务"前缀（如 "子任务 · 第 2/6 轮"） |
| `mini_react:tool_call` / `mini_react:tool_result` | 转对应主类型原样转发（前端 ToolCallToast 直接展示 webfetch 等调用） |
| `mini_react:thought` / `mini_react:done` / `mini_react:error` | 丢弃 |

#### 步骤 8 — 超时控制与执行

```go
timeout := o.subAgentTimeout
if timeout <= 0 {
    timeout = 600
}
subCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
defer cancel()

result, err := react.Run(subCtx, opts.Prompt)
if err == nil {
    return &tool.SubAgentRunResult{
        Success: true,
        State:   "completed",
        Text:    result.FinalContent,
        ConvID:  subConv.ID,
    }
}
```

#### 步骤 9 — 超时/失败部分结果组装（不丢调研成果）

MiniReAct 出错时已返回完整 `ToolCallLog`（工具调用 + 结果）——子循环已完成的调研成果不回传就是浪费。超时/失败也把工具日志摘要回传主 LLM：

```go
state := "failed"
if errors.Is(err, context.DeadlineExceeded) || errors.Is(subCtx.Err(), context.DeadlineExceeded) {
    state = "timeout"
}
return &tool.SubAgentRunResult{
    Success: false,
    State:   state,
    Error:   fmt.Sprintf("子任务%s: %v", state, err),
    Text:    summarizeToolCallLog(result.ToolCallLog),
    ConvID:  subConv.ID,
}
```

`summarizeToolCallLog`（subagent.go 内实现）：每条工具调用输出 `tool.function → success/fail + 结果截断 ~500 字符`，总预算 ~4000 字符（复用 `truncateToolResult`）。task 工具收到 `Success=false` 但 `Text` 非空时，以 `<task state="timeout|failed">` 包裹返回给主 LLM 利用部分成果（见 2.4）。

### 3.3 MiniReAct 扩展（替代原 runSubAgentLoop 方案）

**决策**：不新增第三套循环（原 runSubAgentLoop 建立在 runOneRound 之上）。MiniReAct 已具备子循环所需的一切：同步 ChatSync、原生 FC、强制收敛轮（mini_react.go:194）、工具日志（ToolCallLog）、事件回调（`mini_react:` 命名空间，mini_react.go:61-67）。本阶段只做增量扩展。

#### 3.3.1 MiniReActConfig 新增字段

| 字段 | 类型 | 用途 | 缺省 |
|------|------|------|------|
| `ClientTools` | `[]ClientTool` | 客户端工具定义（browser 等） | nil = 无客户端工具 |
| `ClientBridge` | `*ClientToolBridge` | 客户端工具结果回传桥 | nil = 不分发客户端工具 |
| `ClientToolTimeout` | `time.Duration` | 客户端工具执行超时 | 0 = 60s（与 orchestrator 同款默认） |
| `ConvID` | `string` | 注入 `ExecuteContext`（工具按会话上下文工作） | "" |
| `AllowedTools` | `map[string]bool` | 运行时白名单 guard | nil = 不限 |
| `MsgSink` | `MsgSink` | 可选持久化回调 | nil = 纯内存（profile_worker 不变） |

```go
// MsgSink — MiniReAct 的可选持久化回调。nil = 纯内存运行（profile_worker 现状）。
// RunSubAgent 注入落库实现，子会话消息完整落库（可观测/可调试）。
type MsgSink interface {
    SaveUserMsg(content string)
    SaveAssistantMsg(msg llm.Message)
    SaveToolMsg(toolCallID, content string)
}
```

#### 3.3.2 executeToolCalls 改动

1. **客户端工具分发**：`clientIdx[tc.Tool]` 命中 → 调共享 `clientToolCaller`（见 3.5）
2. **白名单 guard**：`AllowedTools` 非空且 `tc.Tool` 不在内 → 错误结果（防 LLM 幻觉工具名，未来 prompt-eng 扩展路径也用得上）
3. **clientToolsUnavailable fast-fail**：客户端工具超时/断连后置位，本次 Run 内后续客户端工具快速失败（对齐 orchestrator 语义）
4. **`ExecuteContext` 补 `ConvID`**（当前 mini_react.go:277 未传）
5. **客户端工具结果走 `normalizeClientToolData` 预算链**
6. **持久化回调**：Run 开头 `SaveUserMsg`；每轮 assistant 消息 `SaveAssistantMsg`；每条工具结果 `SaveToolMsg`

**后处理天然跳过**：MiniReAct 不触发 Touch / embedding / onChatEnd——子会话天然隐身，无需原方案的 `isSubAgent` 三处跳过（原 3.6 整节删除）。

### 3.4 `buildSubAgentSystemPrompt`

```go
func buildSubAgentSystemPrompt() string {
    var buf bytes.Buffer

    // Layer 1: 环境信息 + 当前时间
    buf.WriteString("你是 xhanHelper 的子 agent。\n")
    buf.WriteString(fmt.Sprintf("当前时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

    // Layer 2: 角色定义（explore 角色 prompt）
    buf.WriteString(explorePrompt)
    buf.WriteString("\n\n")

    // 工具定义不进 prompt：原生 FC 下经 MiniReActConfig.Tools 传入（3.2 步骤 5）
    return buf.String()
}
```

**与主循环 SystemPrompt 的区别**：
- 不经过三层 `promptbuilder.Build`（不注入检索上下文/记忆 packet）
- 原生 FC 下工具定义走 `ChatRequest.Tools`（MiniReAct 配置传入），不进 prompt
- 不含 `GenerateImportantRules`（该规则依赖 tool_retriever 存在）

### 3.5 clientToolCaller 提取（`client_tools.go`）

把 orchestrator 的 `executeClientTool`（orchestrator.go:1361）抽成独立结构，orchestrator 与 MiniReAct 共用，消除重复实现：

```go
// clientToolCaller — 客户端工具执行器（orchestrator / MiniReAct 共用）。
type clientToolCaller struct {
    bridge  *ClientToolBridge
    timeout time.Duration
}

// Call 分派一次客户端工具调用，阻塞等待客户端回传。
// mustSend：指令类事件（client_tool_call）必须送达，由调用方保证阻塞语义（5s 兜底）；
// bestEffort：展示/心跳类事件，非阻塞丢失只影响展示。
// 返回 (result, unavailable)：unavailable=true 表示客户端断连/超时，
// 调用方应置位 fast-fail 标志，本次 Run 内后续客户端工具快速失败。
func (cc *clientToolCaller) Call(
    ctx context.Context,
    mustSend func(*llm.StreamEvent) bool,
    bestEffort func(*llm.StreamEvent),
    userID string,
    call tool.ToolCall,
    mode string,
) (*tool.Result, bool)
```

**行为零变化要求**：orchestrator 改用它后，事件类型/心跳间隔（15s）/超时文案/预算链与现状完全一致——现有 `client_tools_test.go` 用例兜底回归。

两侧接线：
- orchestrator：`mustSend` = 阻塞发送 + 5s 兜底，`bestEffort` = 现有 `sendEvent`（非阻塞）
- MiniReAct：`mustSend` = onEvent 直达（RunSubAgent 的 forwarder 保证阻塞送达语义），`bestEffort` = `emit`

### 3.6 配置（`conf/config.yaml`）

```yaml
subagent:
  explore_max_iterations: 20    # 防死循环兜底（timeout 才是真实预算）；轮次太多任务转不完
  explore_timeout_seconds: 600
```

`config/config.go` 新增：

```go
type SubAgentConfig struct {
    ExploreMaxIterations int `yaml:"explore_max_iterations"`
    ExploreTimeoutSeconds int `yaml:"explore_timeout_seconds"`
}
```

### 3.7 Wiring（`appinit/agents.go`）

`appinit/agents.go`（Orchestrator 构造后）：

```go
// Orchestrator 构造完成后，二次注册 task 工具（依赖 runner 回调，避免循环依赖）
taskTool := tool.NewTaskTool(orch)
reg.Register(taskTool)
```

webfetch 仍在 `appinit/tools.go` 注册（`webfetch.enabled=true` 时，含 `HideTool("webfetch")`，见 1.2）。`OrchestratorConfig` 传入新配置：

```go
SubAgentMaxIterations: cfg.SubAgent.ExploreMaxIterations,
SubAgentTimeoutSecs:   cfg.SubAgent.ExploreTimeoutSeconds,
```

### 3.8 测试

| 测试 | 覆盖点 |
|------|--------|
| `TestDepthCheck_SubAgentRejectsNested` | 父会话 ParentID 链 depth=1 → RunSubAgent 拒绝 |
| `TestDepthCheck_MainAllows` | 主会话 depth=0 → RunSubAgent 通过 |
| `TestResultWrapFormat` | 模拟 runner 返回文本 → task 工具包裹 `<task id state>` → 验证 completed/timeout/failed 三态格式 |
| `TestTaskTool_PartialResultOnFailure` | runner 返回 Success=false + Text 非空 → 包裹 state=timeout 返回；Text 空 → 硬错误 |
| `TestSubAgent_EventForwarding` | 模拟 forwarder 各类事件 → client_tool_call 阻塞送达 / status 加"子任务"前缀 / thought 丢弃 |
| `TestSubAgent_TimeoutSalvagesToolLog` | 子循环 ctx 超时 → 结果含 ToolCallLog 摘要文本 + state=timeout |
| `TestSubAgent_MsgSinkPersists` | MsgSink 注入 → 子会话 user/assistant/tool 消息完整落库 |
| `TestSubAgent_SkipsPostProcessing` | 子会话跑完 → 断言无 Touch/embedding/session_end 任务（MiniReAct 天然不触发） |
| `TestMiniReAct_ClientToolDispatch` | ClientTools 命中 → 走桥分发；回传结果走 normalize 预算链 |
| `TestMiniReAct_ClientToolTimeoutFastFail` | 首个客户端工具超时 → 后续客户端工具快速失败 |
| `TestMiniReAct_WhitelistGuard` | AllowedTools 不含某工具 → 错误结果 |
| `TestSubAgent_ToolsIncludeHiddenWebFetch` | registry 中 webfetch 已 HideTool → 子循环 defs 含 webfetch、主循环 defs 不含 |
| `TestBuildSubAgentSystemPrompt` | 检查含 explorePrompt + 当前时间 |
| `TestWebFetch_HTML2Text` | 含 script/style/br/p/div 的 HTML → 纯文本 |

### 3.9 Phase 18-3 验收

终验（按 CLAUDE.md 主链路分级的完整验收）：

- `make test` 全绿
- 真机测试："帮我调研一下最近 LLM 上下文缓存技术的发展" → 检查：
  1. 对话正常完成（有结论回复）
  2. 会话列表**不出现**"调研 LLM... (explore)" 子会话
  3. 日志中无 `[post_chat_group]` 对子会话的调用（证明 session_end 未触发）
  4. `vector_store` 不出现子会话的消息 ID（证明 embedding 未写入）
  5. 子会话在 DB 中存在（`parent_id` 指向主会话），消息完整落库
- 子会话中 webfetch 和 browser（如 Mac App 在线）能正常使用
- 主会话的 tool_call toast 正确显示 task 工具调用状态

### 3.10 用户介入机制（ask_user）

**动机**：explore 走 browser 时可能遇到验证码/登录墙，需要用户介入才能继续。子 agent 在后台操作浏览器 tab，用户对"需要他"这件事完全无感知——必须要有显式的唤醒机制。**设计原则：子 agent 不主动中断用户，但必须能让用户知道并响应。**

**方案**：`ui__ask_user` 特殊客户端工具——复用现有客户端工具协议（client_tool_call 事件 + tool-result 回传），零新协议。

#### 3.10.1 数据流

```
子循环 LLM 调 ui__ask_user(message)
  → MiniReAct execClientTool（ui 特殊预算：subCtx 剩余时间）
  → client_tool_call SSE → 主循环 forwarder 转发（已有逻辑）
  → 客户端 handleClientToolCall 识别 ui__ask_user → 弹 AskUserBar + 系统通知
  → 用户点 [已处理] → submitToolResult(result: {"answered":true})
  → 用户点 [跳过] / UI 超时 → submitToolResult(error: "用户未处理，已跳过")
  → 子循环 LLM 收到结果：已处理则继续当前操作；跳过则换来源
```

**仅子循环可见**：ui 工具由服务端在 `RunSubAgent` 注入 `SubAgentExt.ClientTools`，主循环 defs 不含它——主 agent 用自然语言对话，不需要该工具。**注入条件**：仅当客户端工具非空（Mac 客户端在线）时注入——纯 Vue 场景无浏览器，ask_user 无意义。

#### 3.10.2 服务端改动

| 文件 | 改动 |
|------|------|
| `agent/client_tools.go` | `buildAskUserDef()`：构造 `ui` server + `ask_user` 函数 def。参数：`message` 必填（展示给用户的话术）、`context` 可选 |
| `agent/subagent.go` | 注入 ui 工具；explorePrompt 加规则（见下） |
| `agent/mini_react.go` | `execClientTool` 对 `ui` server 特判：预算 = subCtx 剩余时间（600s 总预算封顶）而非 120s 客户端默认 |

**explorePrompt 新增规则**：
- 仅在必须用户介入时调用 ask_user（验证码、登录墙等），一次只请求一个用户动作
- message 必须指明哪个页面/tab（如"XX 网站的验证码，请处理"）——多子任务并发时用户才能区分
- 用户跳过或超时后必须换方案，不得重复请求同一动作

**不做硬限次**：浏览器验证码可能反复出现（换页面又遇验证码），子循环允许多次 ask_user。

#### 3.10.3 客户端改动（Mac）

| 文件 | 改动 |
|------|------|
| `ChatViewModel` | 新发布状态 `pendingUserRequests: [UserRequest]`（数组，多 task 并行排队），独立于 toolQueue——**不被文本流清理**（ToolActivityToast 在文本流开始时清 toolQueue 退场，不能承载 ask_user） |
| 新组件 `AskUserBar` | 非模态悬浮条，位置在输入栏上方（与 L3 确认条同区）：图标 + 话术 + 可选回答输入框 + [已处理/发送] [跳过] 按钮；持续驻留直到响应或超时。有输入时主按钮变"发送"（回答随结果回传 LLM，`{"answered":true,"reply":"..."}`）；无输入点"已处理"（`{"answered":true}`）；"跳过"走错误通道 |
| `ChatViewModel+ClientTools.swift` | `ui__ask_user` 分支：发系统通知 + 弹条 + 等待（timeout_secs × 0.9 超时视为跳过）→ submitToolResult |

**多请求行为**：
- 每请求完全独立：callID 隔离、独立 deadline、独立等待 Task（客户端现有 client_tool_call 已按事件开独立 Task）
- **堆叠 + 折叠**：第 1 个完整展示（图标+文案+按钮）；第 2+ 个折叠为"+N 个待处理"，点击展开列表；响应/超时后队列前进
- **通知策略**：只有第 1 个请求发系统通知（带声音）；后续只弹条不发通知，避免通知轰炸；队列清空后新请求到达再发一次
- 同一子循环连续 ask_user 天然串行（等待期间子循环阻塞），不会同时出现两个
- 不设请求数硬上限——响应/超时自然消化

**注意（既有问题非新增）**：浏览器是单例（`BrowserToolProvider.shared`），多子循环并发操作浏览器本身可能互踩 tab——ask_user 的 message 必须带页面上下文以帮助用户区分（见 explorePrompt 规则）。

#### 3.10.4 测试

| 测试 | 覆盖点 |
|------|--------|
| `TestAskUserDef_InjectedOnlyWithClientTools` | 客户端工具非空才注入 ui 工具；空则无 |
| `TestMiniReAct_AskUserBudget` | ui 工具预算 = subCtx 剩余时间（<120s 时取剩余值，无 deadline 时用默认） |
| `TestAskUser_DefStructure` | buildAskUserDef 参数结构（message 必填/context 可选） |
| 真机（Xcode 验证） | 验证码页面 → 弹条 + 通知出现 → [已处理] 子循环继续 / [跳过] 子循环换来源 / UI 超时视为跳过 |

#### 3.10.5 明确不做

- ask_user 硬限次（验证码反复出现，需多次介入）
- Vue 前端适配（无浏览器场景注入条件已排除）

#### 3.10.6 真机反馈修复（首轮实测）
首轮真机三个并行调研任务的教训与修复：

| 问题 | 现象 | 修复 |
|------|------|------|
| P0 失败不传播 | open 返回 success=true 但内嵌 error:timeout/Cloudflare 验证页，LLM 误判成功无效重试 10 次耗尽 tab 配额 | 客户端：加载失败/超时、拦截页标题（Attention Required/Cloudflare/403）、wait_for_load 超时一律走 serverError 硬失败（BrowserToolProvider） |
| P0 拦截止损 | 对 Cloudflare/403 结构性拦截站不死心反复重试 | 服务端 explorePrompt 规则：识别拦截特征 → 立即换源；同域名失败 2 次禁试；tab 节制（后台打开/读完即关/活跃 ≤3） |
| P0 ask_user 被 fast-fail 拦截 | 浏览器断连后 ask_user 也报"客户端不可用"，子任务想求助都找不到人 | 服务端：fast-fail 对 ui 工具豁免（ask_user 不依赖浏览器链路，断连由 5s 送达超时兜底） |
| P1 浏览器并发竞争 | 三任务并行共享 BrowserToolProvider 单例，tab 配额全局共享，可能压垮 WebView 进程 | 客户端：浏览器工具执行串行队列（持锁），所有调用排队 |
| 成本中心缺失 | 子循环 LLM 调用未记录成本 | 服务端：subCtx 注入 Scenario（main_chat/sub_agent_run，ConvID=子会话，Meta 含 subagent_type/description） |
| 轮次过大 | 50 轮兜底太大，任务转不完 | explore_max_iterations 50 → 20 |
| 数据边界 | 子任务可读记忆/历史/摘要（私密对话数据） | 白名单收窄：仅保留笔记检索（search_notes_v2）；unified_search/search_memories_v2/message_search 移除；prompt 声明边界 |
| tab 配额不友好 | 10 tab 上限对调研任务太紧 | 客户端配额 10 → 20；子任务结束时服务端清理本次打开的全部标签（collectTabIDsFromLog + close_tab，只清自己的，10s/tab 超时，失败忽略由 housekeeping 兜底） |
| 会话共享踩踏（加码验证升级） | 并行子任务共享同一浏览器实例：list_tabs 看到所有任务的标签，某任务的 close 把其他任务的标签全清空（贝壳 4 标签被清、小区页均价丢失）——这才是此前"客户端本地工具不可用"的真正源头 | **标签归属隔离**：client_tool_call payload 带 `owner`（main / subagent:\<convID\>）；客户端 open 记录归属、list_tabs 标注 owner、**close_tab 拒绝关闭其他归属的标签**（报错说明归属，LLM 据此止损）；explorePrompt 加"只操作自己的标签"规则；用户主动关闭（Cmd+W/UI）绕过保护 |

**后处理防线**：子会话消息（MsgSink 落库）带 `IsSubAgent` 标记，MessageRepo 按用户扫描的查询（CountRecent/SearchMessagesByCursor/CountMessages——反射活动统计与 message_search 聚合）统一排除，杜绝子会话消息进入任何后台后处理。

### 3.11 子任务活动面板（SubagentPanel）

**动机**：真机反馈"子 agent 在后台咔咔干活，但反馈路径太少"（3.10.6）。opencode 等价能力是"子会话视图"——本方案取其轻量版：状态行 + 展开消息列表。

**交互**（用户拍板）：
- **上推布局**：面板在输入栏上方（AskUserBar 之下），不遮挡聊天区
- 每个子任务一行状态：图标（⏳ 转圈/✓ 完成/⚠ 超时）+ 描述 + 轮次 + 最新动作
- 点击展开为整宽卡片：子会话消息列表（轻量行：👤/🤖/🔧 图标 + 文本），内部滚动限高 300pt
- 多任务垂直堆叠；完成/超时行收起为单行摘要
- 展开态收到新事件 → 防抖 1s 重拉 DB 消息（轮数有限，幂等覆盖，不做取消）

**数据流**：

```
服务端：
  RunSubAgent 发 subagent:start（conv_id/description/state=started）
  forwarder 统一打包 subagent 事件（conv_id/description/type/content）：
    status（轮次/心跳）/ tool_call / tool_result
  defer 发 subagent:end（state=completed|timeout|failed + 结果摘要）
  controller chat.go case "subagent" 原样透传
客户端：
  handleSubagentEvent → subagentSessions（按 conv_id 归集）
  状态行更新（round/latestAction/summary）；展开态防抖重拉
  toggleSubagentExpand → reloadSubagentMessages（GET messages 现成接口，MsgSink 已落库）
```

**平铺转发移除**：原 forwarder 将子循环事件平铺为 status/tool_call/tool_result（主 toast 展示）已改——子任务活动统一走 subagent 事件，主循环自己的工具 toast 不受影响。client_tool_call 保持原通道（浏览器指令/ask_user 必须送达）。

**测试**：TestSubAgent_EventForwarding（subagent 事件打包格式）、TestRunSubAgent_EmitsStartEnd（start/end 事件与 state）。

---

## 四、Phase 18-4：端到端验收 + 收尾

**目标**：确认系统级正确性，补齐遗漏，产出缓存命中率对比数据。

**依赖**：Phase 18-3 完成。

### 4.1 全链路回归

| 场景 | 期望 |
|------|------|
| 单 task 调研 | 子会话创建 → 独立循环 → 结果回传 → 主 agent 总结 |
| 并行 task ×2 | 两个子会话同时跑 → 等待全部完成 → 主 agent 汇总两个结论 |
| 调研 + 后续追问 | 调研完成后用户追问"第二点展开说说" → 主会话正常继续（不触发子会话后处理） |
| 浏览器不可用降级 | Mac App 离线 → explore 调 browser 失败 → webfetch 自动兜底 |
| webfetch 抓非 HTML | explore 调 webfetch 抓 JSON → 返回错误 → explore 换 browser |
| 子会话不重现 | 跨日重新打开主会话 → 子会话不出现在列表 → 子会话消息可通过 DB 查看 |
| 多用户并发 | 两个用户同时调研 → 各自的子会话独立、client_bridge 不串号 |

### 4.2 缓存命中率对比（可选但推荐）

选 2-3 个典型调研任务（如"调研 Vue 3.5 新特性"、"调研 DeepSeek 上下文缓存机制"），分两轮测试：

| 轮次 | 方法 | 记录 |
|------|------|------|
| A | Web 内容直接进主会话（用户手动粘贴网页内容到聊天） | 每次 LLM 请求的 `PromptCacheHitTokens` / `PromptCacheMissTokens` |
| B | 子 agent 模式（explore 调研） | 同指标 |

预期：主会话在方案 B 下前缀缓存更稳定（不含网页原文的上下文在多次对话间大部分一致），子会话的 miss 集中发生在一次性调研周期内。

### 4.3 清理项

| 项目 | 说明 |
|------|------|
| Bocha 搜索 | 确认 `SearchConfig` 字段移除、注册点移除、`SearchUsageRepo` 注入移除；保留表结构和模型 |
| 前端 task toast | 可选：tool_call 事件里 tool="task" 时前端展示"子任务进行中"→ 完成时显示结果摘要（不强制，Phase 18-4 可跳过） |
| 文档 | `CLAUDE.md` 工具列表加 `task` 和 `webfetch`；更新架构图 |

### 4.4 Phase 18-4 验收

- `make test` 全绿
- 至少 3 次真机"调研 XX"完整链路通过
- 缓存命中率对比数据归档（仅记录，不作为 pass/fail 条件）

---

## 五、不做事项（显式排除）

| 项目 | 原因 |
|------|------|
| mailbox 下掉 | 单独一期：涉及 reflection/theme 管道重构（`theme.go`、`reflection.go`、前端 mailbox 页面、API 路由） |
| task_id 续跑 | Phase 19：子会话继续追加任务，需前端配合 |
| background 后台模式 | Phase 19：子任务后台跑 + 完成注入 synthetic 消息 |
| general 子 agent | Phase 19：带写权限的多步执行子 agent |
| 子循环内 compaction | 留待探索：调研任务足够长时再考虑压缩早期轮次 |
| 前端子任务 UI | Phase 19：子任务进度面板、结果展开/收起 |
| 孤儿子会话 | 客户端超时重发同一条消息 → 创建新子会话，旧的成孤儿（列表不可见）。explore 只读，损失=LLM 花费+DB 行数。Phase 19 考虑：派发前复用同 ParentID 未完成子会话或标记废弃 |
| 并行 task 无 429 重试 | 多个子循环并发触发 DeepSeek 限流时直接失败（部分成果经 state=failed 回传）。个人助手量级可接受，暂不做重试 |

---

## 六、附录

### A. 主 agent ↔ 子 agent 通信对照

| 环节 | opencode | xhanHelper |
|------|----------|------------|
| 触发 | 原生 FC 调 task 工具 | Flash JSON `tool_calls` 或原生 FC |
| 权限 | Permission.ask + 子权限推导 | 白名单双层拦截（工具定义过滤 + MiniReAct AllowedTools 运行时 guard） |
| 会话 | 新建独立 Session(parentID) | 新建子 Conversation(ParentID + Extra.SubAgentType) |
| 上下文 | 全新，仅 prompt 文本 | 全新，仅任务 prompt |
| 进度回传 | 子会话事件带 sessionID 流式推送 | 复用主 SSE 事件类型转发（status/tool_call/tool_result + client_tool_call 阻塞送达），前端零改动 |
| 结果 | `<task id state>` XML 包裹 | 同左；超时/失败带部分成果，state=timeout/failed |
| 并行 | 一条消息多个 task 并发执行 | 复用现有 executeConcurrently |
| 深度限制 | subagent_depth 配置 | 写死 1 层（ParentID 链 ≥1 拒绝） |
| 用户介入 | — | `ui__ask_user` 客户端工具（仅子循环注入），AskUserBar + 系统通知（见 3.10） |
| 续跑 | task_id 复用子会话 | Phase 19 |
| 后台模式 | background job + 完成注入 | Phase 19 |

### B. 文件改动汇总

| 阶段 | 新增 | 修改 |
|------|------|------|
| Phase 18-1 | `tool/webfetch.go`、`tool/webfetch_test.go`、`tool/registry_hidden_test.go` | `appinit/tools.go`、`tool/registry.go`、`conf/config.yaml`、`internal/config/config.go`、`internal/app/init_repos.go`、删除 `tool/search.go` `tool/search_bocha.go` |
| Phase 18-2 | `tool/subagent.go`、`tool/task.go` | `model/conversation.go`、`tool/tool.go`、`service/conversation_repo.go`、`agent/orchestrator.go` |
| Phase 18-3 | `agent/subagent.go`、`agent/subagent_test.go` | `agent/client_tools.go`、`agent/mini_react.go`、`agent/orchestrator.go`、`internal/config/config.go`、`appinit/agents.go`、`conf/config.yaml` |
| Phase 18-3 客户端（ask_user，见 3.10） | `client/.../Views/AskUserBar.swift` | `client/.../ChatViewModel.swift`、`client/.../ChatViewModel+ClientTools.swift` |
| Phase 18-4 | — | 文档更新 |
