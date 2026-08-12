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
| `Permission.merge` + 子 deny 规则 | 工具白名单过滤（`RunRequest.ToolWhitelist`） |
| `renderOutput`（`<task id state>` XML） | 同格式 |
| `BackgroundJob` + `inject` 后台通知 | Phase 19（本期不做） |
| `subagent_depth` 配置 | 写死 1 层 |

### 0.4 分阶段总览

```
Phase 18-1 (webfetch + Bocha 下掉)
     │
     ├──→ Phase 18-2 (SubAgent 基础设施：模型/接口/类型)
     │         │
     │         └──→ Phase 18-3 (explore 子 agent 完整实现)
     │                   │
     │                   └──→ Phase 18-4 (端到端验收 + 收尾)
     │
     └── (Phase 18-2 可并行 review，但推荐串行实施)
```

---

## 一、Phase 18-1：webfetch 工具 + Bocha 搜索下掉

**目标**：主会话获得服务端直抓 URL 能力；下掉已停止付费的 Bocha 搜索。

**依赖**：无。

**新增文件**：

| 文件 | 说明 |
|------|------|
| `server/internal/tool/webfetch.go` | webfetch 工具实现 |
| `server/internal/tool/webfetch_test.go` | 单元测试 |

**修改文件**：

| 文件 | 改动 |
|------|------|
| `server/internal/appinit/tools.go` | 注册 webfetch；移除 Bocha 搜索注册 |
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

### 1.2 fetchURL 实现

流程：

1. 解析 URL 参数
2. 创建 GET 请求，设置 `User-Agent: "Mozilla/5.0 (compatible; xhanHelper/1.0)"`
3. 若 HTTP 错误（404/50x）→ 返回错误带状态码
4. 若 `Content-Type` 非 `text/html` 或 `text/plain` → 返回 `"内容类型为 <type>，请改用 browser 工具访问该页面"`
5. 读 body，`io.LimitReader` 限制 `t.maxBytes`
6. 根据 Content-Type：
   - `text/html` → html2text（见 1.3）
   - `text/plain` → 直接返回
7. 截断到 `t.maxBytes`（如果 html2text 后仍超限，尾部加 `"[内容已截断]"`）
8. 返回 JSON: `{"url": "...", "title": "<title 内容或空>", "text": "<正文>"}`

### 1.3 html2text 提取

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

### 1.4 配置

`conf/config.yaml` 新增：

```yaml
webfetch:
  timeout_seconds: 15
  max_bytes: 51200
```

`config/config.go` 增加对应结构体字段，从 YAML 加载。

### 1.5 Bocha 搜索移除

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

### 1.6 测试（`tool/webfetch_test.go`）

| 用例 | 输入 | 预期 |
|------|------|------|
| 正常 HTML | 含 `<title>`、`<p>`、`<script>`、`<style>` 的 HTML | 返回纯文本，不含 script/style 内容，含 title |
| 纯文本 | `Content-Type: text/plain` | 直通返回 |
| 非 HTML 类型 | `Content-Type: application/json` | 错误 "请改用 browser 工具" |
| 404 | HTTP 404 | 错误含状态码 |
| 超时 | 无响应的服务器 | 超时错误 |
| 超限截断 | body > maxBytes | 截断末尾加 "[内容已截断]" |
| 空白/空页面 | 空白 HTML | 返回空 text |

### 1.7 Phase 18-1 验收

- `go test ./tool/ -run WebFetch -v` 绿
- 真机说一句 "帮我把 https://example.com 的内容总结一下" → 主 agent 调 webfetch → 返回文本 → 总结

---

## 二、Phase 18-2：SubAgent 基础设施（模型/接口/类型）

**目标**：所有新类型、接口、骨架代码就绪，`go build ./...` 通过，运行时不改变任何现有行为（task 工具不注册）。

**依赖**：Phase 18-1（webfetch 已存在于 registry，Phase 18-2 不依赖它但并行开发友好）。

**新增文件**：

| 文件 | 说明 |
|------|------|
| `server/internal/tool/subagent.go` | `SubAgentRunner` 接口 + `SubAgentRunOptions` + `SubAgentRunResult` + `ClientToolInfo` |
| `server/internal/tool/task.go` | task 工具骨架（Execute 空实现，不注册） |

**修改文件**：

| 文件 | 改动 |
|------|------|
| `server/internal/model/conversation.go` | `ConversationExtra` 加 `SubAgentType` |
| `server/internal/tool/tool.go` | `ExecuteContext` 加 `ClientTools []ClientToolInfo` |
| `server/internal/service/conversation_repo.go` | `ListByUser` 加 `parent_id IS NULL` |
| `server/internal/agent/orchestrator.go` | `RunRequest` 加 `ToolWhitelist`；`executeConcurrently` 构造 `ec` 时填入 `ClientTools`；`executeConcurrently` 加 `toolWhitelist` 参数 + 过滤逻辑 |

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
    Text    string // 子 agent 最终回复（未包裹 <task> 标签，由 task 工具包裹）
    ConvID  string // 子会话 ID
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

### 2.4 `tool/task.go` — 骨架（新文件）

```go
package tool

import (
    "encoding/json"
    "fmt"
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
    return `启动一个子 agent 独立完成复杂任务，执行完毕后返回结果。

何时使用：
- 需要联网调研多步信息（搜索 → 抓取多个网页 → 提炼结论）
- 多个独立任务可并行执行
- 任务量大、需要独立上下文避免污染当前对话

何时不用：
- 单次简单的查询/计算/查笔记 → 直接用对应工具
- 任务短小、一两个工具调用就能完成

用法：
- 可一次并发多个 task（利用并行加速）
- 子任务的执行过程和结果对用户不可见，你必须用自然语言总结转发给用户
- prompt 要写详细：明确要做什么、期望返回什么、用什么格式`
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
                    "description": "任务简短描述（3-5 词，如 调研XX技术）",
                },
                "subagent_type": map[string]interface{}{
                    "type":        "string",
                    "description": "子 agent 类型，当前可用：explore（联网调研，只读）",
                },
                "prompt": map[string]interface{}{
                    "type":        "string",
                    "description": "给子 agent 的完整任务描述。必须自包含（子 agent 不共享当前对话上下文）。写明要调研什么、要返回什么、遵循什么格式。",
                },
            },
            "required": []interface{}{"description", "subagent_type", "prompt"},
        },
    }}
}

func (t *TaskTool) Execute(ec *ExecuteContext, funcName string, args json.RawMessage) *Result {
    // Phase 18-2: 骨架，直接返回错误（未注册，不会触发）
    if t.runner == nil {
        return &Result{Success: false, Error: "task 工具尚未启用（runner 未注入）"}
    }
    // Phase 18-3 实现（见第三节）
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

    if !result.Success {
        return &Result{Success: false, Error: result.Error}
    }

    // 包裹结果（opencode renderOutput 风格）
    output := fmt.Sprintf(
        `<task id="%s" state="completed">
<summary>子任务完成: %s</summary>
<task_result>
%s
</task_result>
</task>`,
        result.ConvID, params.Description, result.Text,
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

### 2.6 `agent/orchestrator.go` — 四处修改

#### 2.6.1 `RunRequest` 加字段

```go
type RunRequest struct {
    // ... 现有字段 ...
    ToolWhitelist []string // 新增：子循环工具白名单，nil=全量（主循环行为不变）
}
```

#### 2.6.2 `executeConcurrently` 签名变化

```go
// 前：
func (o *Orchestrator) executeConcurrently(ctx context.Context,
    events chan<- *llm.StreamEvent, userID, convID string, ...)

// 后：
func (o *Orchestrator) executeConcurrently(ctx context.Context,
    events chan<- *llm.StreamEvent, userID, convID string,
    ..., toolWhitelist []string, clientTools []tool.ClientToolInfo)
```

#### 2.6.3 EC 构造注入 ClientTools

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

#### 2.6.4 白名单运行时过滤

服务端工具路径（`registry.ExecuteByName` 之前）：

```go
// 构建 whitelist map 一次
whitelistMap := make(map[string]bool, len(toolWhitelist))
for _, name := range toolWhitelist {
    whitelistMap[name] = true
}

// 在 ec := ... 之后立即加：
if len(toolWhitelist) > 0 && !whitelistMap[call.Tool] {
    r = &tool.Result{Success: false,
        Error: fmt.Sprintf("子任务无权调用工具 '%s'（不在白名单中）", call.Tool)}
    results[idx] = toolExecResult{tc: call, result: r}
    return // 跳过执行
}
```

客户端工具路径同理（`executeClientTool` 之前）：

```go
if len(toolWhitelist) > 0 && !whitelistMap[tc.Tool] {
    results[idx] = toolExecResult{tc: tc, result: &tool.Result{Success: false,
        Error: fmt.Sprintf("子任务无权调用客户端工具 '%s'（不在白名单中）", tc.Tool)}}
    continue
}
```

#### 2.6.5 `finalizeRound` 传递白名单

`finalizeRound` 已有 `req *RunRequest` 参数。在调用 `executeConcurrently` 处：

```go
// 构造 tool.ClientToolInfo 切片
clientToolInfos := make([]tool.ClientToolInfo, len(req.ClientTools))
for i, ct := range req.ClientTools {
    clientToolInfos[i] = tool.ClientToolInfo{
        ServerName:  ct.ServerName,
        Name:        ct.Name,
        Description: ct.Description,
        Parameters:  ct.Parameters,
        IsReadOnly:  ct.IsReadOnly,
    }
}

results := o.executeConcurrently(
    ctx, events, req.UserID, req.ConversationID,
    ..., req.ToolWhitelist, clientToolInfos,
)
```

### 2.7 Phase 18-2 验收

- `go build ./...` 编译通过
- `go test ./...` 无回归
- 会话列表 API 返回正常（前端看不到子会话——即使测试库有 ParentID 非 nil 的记录）

---

## 三、Phase 18-3：explore 子 agent 完整实现

**目标**：主循环可调 task 工具 → explore 在独立上下文中完成联网调研 → `<task>` 结果返回主循环。

**依赖**：Phase 18-1（webfetch 必须在 registry 中）+ Phase 18-2（接口/类型/白名单字段就绪）。

**新增文件**：

| 文件 | 说明 |
|------|------|
| `server/internal/agent/subagent.go` | explore 角色定义 + 白名单 + `RunSubAgent` + `runSubAgentLoop` + `buildSubAgentSystemPrompt` |
| `server/internal/agent/subagent_test.go` | 白名单过滤、深度检查、结果包裹、后处理跳过测试 |

**修改文件**：

| 文件 | 改动 |
|------|------|
| `server/internal/agent/orchestrator.go` | `mkLlmChatReq` 按 ToolWhitelist 过滤工具；`runReActLoop` 的 Touch/embedding/onChatEnd 加 `isSubAgent` 条件跳过 |
| `server/internal/appinit/tools.go` | Phase 18-2 注册占位移除（task 工具改在 agents.go 二次注册） |
| `server/internal/appinit/agents.go` | Orchestrator 构造后 `registry.Register(taskTool)` |
| `server/conf/config.yaml` | 加 `subagent:` 配置节 |

### 3.1 explore 角色 prompt（`agent/subagent.go`）

```go
const explorePrompt = `你是联网调研专家，擅长通过网页抓取与本地检索收集信息并提炼结论。

能力：
- 用 webfetch 抓取具体 URL 的正文（首选，快且不依赖客户端）
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
var exploreWhitelist = []string{
    "webfetch",
    "browser",
    "unified_search",
    "search_memories_v2",
    "search_notes_v2",
    "message_search",
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

#### 步骤 5 — 构造子 RunRequest

```go
maxIter := o.subAgentMaxIterations
if maxIter <= 0 {
    maxIter = 6
}
subReq := &RunRequest{
    UserID:             userID,
    ConversationID:     subConv.ID,
    Messages:           nil,            // 子循环 buildUserMessage
    DeepMode:           false,          // 不用 Pro
    UserMessageContent: opts.Prompt,
    AgentSystemPrompt:  explorePrompt,
    SkipRetrieval:      true,           // 不注入检索上下文
    MaxIterations:      maxIter,
    ClientTools:        agentClientTools,
    ToolWhitelist:      exploreWhitelist,
    Mode:               model.ConvModeWork,
}
```

#### 步骤 6 — 构建子循环 events 代理

```go
subEvents := make(chan *llm.StreamEvent, 512)
go func() {
    for se := range subEvents {
        // 只转发 client_tool_call 到主 SSE（浏览器工具需要客户端监听）
        if se.Type == "client_tool_call" && opts.MainEvents != nil {
            select {
            case opts.MainEvents <- se:
            default:
            }
        }
        // 其余事件丢弃（子循环进度不暴露给用户）
    }
}()
```

#### 步骤 7 — 超时控制

```go
timeout := o.subAgentTimeout
if timeout <= 0 {
    timeout = 300
}
subCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
defer cancel()
```

#### 步骤 8 — 执行核心循环

```go
result := o.runSubAgentLoop(subCtx, subEvents, subReq)
close(subEvents)

if !result.Success && result.Error == "" {
    result.Error = "子任务超时"
}
return result
```

### 3.3 `runSubAgentLoop` — 独立子循环

```go
func (o *Orchestrator) runSubAgentLoop(
    ctx context.Context,
    events chan<- *llm.StreamEvent,
    req *RunRequest,
) *tool.SubAgentRunResult {
```

**完整实现**（不经过 `runReActLoop`，完全独立）：

```go
    // 1. 构建简化 system prompt（角色 + 白名单工具定义，全量内联）
    systemPrompt := buildSubAgentSystemPrompt(o.registry, req)

    // 2. 创建并落库第一条用户消息（走 buildUserMessage 以保证 ID 和 XML 结构完整）
    userMsg := o.buildUserMessage(ctx, req, nil)
    if err := o.msgRepo.Create(ctx, userMsg); err != nil {
        return &tool.SubAgentRunResult{Success: false, Error: err.Error()}
    }
    // 注意：不调用 saveUserMsgEmbedding / convRepo.Touch —— 子会话不做后处理

    // 3. 初始化独立 state
    state := &reactState{
        msgs: []llm.Message{{Role: "user", Content: userMsg.XMLContext}},
    }
    client := o.pickClient(req) // DeepMode=false → Flash

    // 4. 循环 runOneRound
    var lastText string
    for round := 0; round < req.MaxIterations; round++ {
        if ctx.Err() != nil {
            lastText = extractLastAssistantText(state)
            return &tool.SubAgentRunResult{
                Success: false,
                Error:   "子任务超时",
                Text:    lastText,
                ConvID:  req.ConversationID,
            }
        }
        done := o.runOneRound(ctx, events, client, req, systemPrompt, nil, state, round)
        if done {
            break
        }
    }

    // 5. 取最终回复文本
    text := extractLastAssistantText(state)
    return &tool.SubAgentRunResult{
        Success: true,
        Text:    text,
        ConvID:  req.ConversationID,
    }
}

// extractLastAssistantText 从 state.msgs 中提取最后一条 assistant 消息的文本
func extractLastAssistantText(state *reactState) string {
    for i := len(state.msgs) - 1; i >= 0; i-- {
        if state.msgs[i].Role == "assistant" && state.msgs[i].Content != "" {
            return state.msgs[i].Content
        }
    }
    return ""
}
```

**关键差异 vs 主循环 `runReActLoop`**：
- 无 `onChatEnd` defer（不触发 session_end 延迟任务）
- 无 `saveUserMsgEmbedding` 调用（子会话消息不嵌入向量库）
- 无 `convRepo.Touch` 调用（子会话时间戳不更新，进一步隐身）
- 无 `retrieveResult`（`SkipRetrieval=true`）
- 用独立 `reactState`

### 3.4 `buildSubAgentSystemPrompt`

```go
func buildSubAgentSystemPrompt(registry *tool.Registry, req *RunRequest) string {
    var buf bytes.Buffer

    // Layer 1: 环境信息 + 当前时间
    buf.WriteString("你是 xhanHelper 的子 agent。\n")
    buf.WriteString(fmt.Sprintf("当前时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

    // Layer 2: 角色定义
    buf.WriteString(req.AgentSystemPrompt) // explorePrompt
    buf.WriteString("\n\n")

    // Layer 3: 工具定义（全量内联，不用 tool_retriever）
    toolPrompt := registry.GeneratePromptFor(req.ToolWhitelist)
    buf.WriteString(toolPrompt)

    return buf.String()
}
```

**与主循环 SystemPrompt 的区别**：
- 不经过三层 `promptbuilder.Build`（不注入检索上下文/记忆 packet）
- 不走 `tool_retriever` 机制（白名单 8 个工具直接全量内联）
- 不含 `GenerateImportantRules`（该规则依赖 tool_retriever 存在）

### 3.5 `mkLlmChatReq` — 按 ToolWhitelist 过滤

```go
func (o *Orchestrator) mkLlmChatReq(req *RunRequest, systemPrompt string,
    msgs []llm.Message, finalRound bool) *llm.ChatRequest {
    // ... 现有逻辑 ...
    var tools []llm.ToolDefinition
    if !finalRound && o.useNativeFC {
        tools = o.registry.GetNativeFCTools()
        // 新增：按白名单过滤（仅子循环生效，主循环 whitelist=nil → 全量）
        if len(req.ToolWhitelist) > 0 {
            tools = filterToolsByWhitelist(tools, req.ToolWhitelist)
        }
        tools = append(tools, buildClientToolDefs(req.ClientTools, o.registry)...)
    }
    // prompt eng 路径：system prompt 里已经用 GeneratePromptFor 过滤过，这里不需要改
    // ... 其余逻辑不变 ...
}

func filterToolsByWhitelist(tools []llm.ToolDefinition, whitelist []string) []llm.ToolDefinition {
    wl := make(map[string]bool, len(whitelist))
    for _, name := range whitelist {
        wl[name] = true
    }
    filtered := make([]llm.ToolDefinition, 0, len(tools))
    for _, t := range tools {
        if wl[t.Name] {
            filtered = append(filtered, t)
        }
    }
    return filtered
}
```

### 3.6 `runReActLoop` 后处理跳过

在 `orchestrator.go` 中 `runReActLoop` 加入子会话检测（三处跳过）：

```go
func (o *Orchestrator) runReActLoop(ctx context.Context, events chan<- *llm.StreamEvent, ...) {
    // 新增：检测是否为子会话
    extra, _ := o.convRepo.GetExtra(ctx, req.ConversationID)
    isSubAgent := extra != nil && extra.SubAgentType != ""

    // ...

    // Touch：仅主会话
    if !isSubAgent {
        util.SafeGo(func() { o.convRepo.Touch(ctx, req.ConversationID) })
    }

    // Embedding：仅主会话
    if !isSubAgent {
        util.SafeGo(func() {
            o.saveUserMsgEmbedding(userMsg.ID, queryVec, req.UserID, req.ConversationID)
        })
    }

    // onChatEnd：仅主会话
    if !isSubAgent && o.onChatEnd != nil {
        defer o.onChatEnd(req.UserID, req.ConversationID, userMsg.ID)
    }

    // ... 继续循环 ...
}
```

### 3.7 Wiring（`appinit/tools.go` + `appinit/agents.go`）

`appinit/agents.go`（Orchestrator 构造后）：

```go
// Orchestrator 构造完成后，二次注册 task 工具（依赖 runner 回调，避免循环依赖）
taskTool := tool.NewTaskTool(orch)
reg.Register(taskTool)
```

`appinit/tools.go`：Phase 18-2 如有 task 占位注册行，移除。webfetch 仍在 tools.go 正常注册。

`appinit/router.go` 或 `cmd/xhan-helper/main.go`：`OrchestratorConfig` 传入新配置：

```go
SubAgentMaxIterations: cfg.SubAgent.ExploreMaxIterations,
SubAgentTimeoutSecs:   cfg.SubAgent.ExploreTimeoutSeconds,
```

### 3.8 配置（`conf/config.yaml`）

```yaml
subagent:
  explore_max_iterations: 6
  explore_timeout_seconds: 300
```

`config/config.go` 新增：

```go
type SubAgentConfig struct {
    ExploreMaxIterations int `yaml:"explore_max_iterations"`
    ExploreTimeoutSeconds int `yaml:"explore_timeout_seconds"`
}
```

### 3.9 测试（`agent/subagent_test.go`）

| 测试 | 覆盖点 |
|------|--------|
| `TestWhitelistFilter_ServerTools` | 构造 tools list + whitelist → 验证白名单外的 tool 被过滤 |
| `TestWhitelistFilter_ClientTools` | 同上，client tools 路径 |
| `TestExecuteConcurrently_WhitelistRejection` | whitelist=["webfetch"] + tool_call=["bookkeeping"] → 返回错误 |
| `TestDepthCheck_SubAgentRejectsNested` | 父会话 ParentID 链 depth=1 → RunSubAgent 拒绝 |
| `TestDepthCheck_MainAllows` | 主会话 depth=0 → RunSubAgent 通过 |
| `TestResultWrapFormat` | 模拟 runner 返回文本 → task 工具包裹 `<task id state>` → 验证格式 |
| `TestSubAgent_SkipsPostProcessing` | 创建子会话 → 跑子循环 → 断言无 Touch/embedding/session_end 任务 |
| `TestBuildSubAgentSystemPrompt` | 调用 buildSubAgentSystemPrompt → 检查含 explorePrompt + 8 个工具定义 |
| `TestWebFetch_HTML2Text` | 含 script/style/br/p/div 的 HTML → 纯文本 |
| `TestSubAgentLoop_ExitOnMaxIter` | 循环到 maxIter → 正常退出 + 捕获最后文本 |

### 3.10 Phase 18-3 验收

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

---

## 六、附录

### A. 主 agent ↔ 子 agent 通信对照

| 环节 | opencode | xhanHelper |
|------|----------|------------|
| 触发 | 原生 FC 调 task 工具 | Flash JSON `tool_calls` 或原生 FC |
| 权限 | Permission.ask + 子权限推导 | 白名单过滤（mkLlmChatReq + executeConcurrently 双点拦截） |
| 会话 | 新建独立 Session(parentID) | 新建子 Conversation(ParentID + Extra.SubAgentType) |
| 上下文 | 全新，仅 prompt 文本 | 全新，仅任务 prompt |
| 进度回传 | 子会话事件带 sessionID 流式推送 | 只转发 client_tool_call（浏览器工具必需）；其余事件丢弃 |
| 结果 | `<task id state>` XML 包裹 | 同左 |
| 并行 | 一条消息多个 task 并发执行 | 复用现有 executeConcurrently |
| 深度限制 | subagent_depth 配置 | 写死 1 层（ParentID 链 ≥1 拒绝） |
| 续跑 | task_id 复用子会话 | Phase 19 |
| 后台模式 | background job + 完成注入 | Phase 19 |

### B. 文件改动汇总

| 阶段 | 新增 | 修改 |
|------|------|------|
| Phase 18-1 | `tool/webfetch.go`、`tool/webfetch_test.go` | `appinit/tools.go`、`conf/config.yaml` |
| Phase 18-2 | `tool/subagent.go`、`tool/task.go` | `model/conversation.go`、`tool/tool.go`、`service/conversation_repo.go`、`agent/orchestrator.go` |
| Phase 18-3 | `agent/subagent.go`、`agent/subagent_test.go` | `agent/orchestrator.go`、`appinit/tools.go`、`appinit/agents.go`、`conf/config.yaml` |
| Phase 18-4 | — | 文档更新 |
