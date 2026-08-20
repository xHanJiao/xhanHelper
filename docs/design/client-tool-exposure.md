# 客户端工具纳入渐进式暴露

## 背景与动机

`progressive-tool-disclosure.md` 落地后，服务端工具已按函数粒度走 `expose_tools.enable` 渐进暴露，大幅降低 LLM 请求 token 量。但**客户端工具**（browser 等，Mac 客户端 MCP 上报）仍被 `mkLlmChatReq`（`orchestrator.go:1036`）无条件注入请求体 tools 字段——客户端工具数量多、schema 大（曾触发 `maxClientTools=50` 截断），token 开销与服务端工具同量级却绕过暴露机制。

目标：客户端工具纳入同一套 function-granularity exposure——目录入 system prompt → LLM 按函数粒度 `expose_tools.enable` → 下一轮起只注入已暴露函数定义。与 `expose_tools` 的既有拒绝行为（`"browser"` 被 reject）一并消除。

## 关键事实

1. **暴露只 gate LLM 可见性**：`executeConcurrently` 的 clientIdx 分发与暴露无关，已暴露的工具在下一轮可正常执行；未暴露只是不注入 tools 字段。
2. **持久化自动兼容**：`exposedTools` 全存 `组名.函数名`（如 `browser.browser_navigate`），`loadExposure/persistExposure` 零改动即可覆盖客户端工具；客户端掉线后过期条目被 `filterExposedFuncDefs` 自然丢弃。
3. **客户端工具目录必须入 prompt**：一旦停止无条件注入，LLM 从请求体再也看不到 browser——目录（组名+描述+函数名，无 schema）是它发现客户端工具的唯一渠道。
4. **`buildClientToolDefs` 已是单一校验/排序来源**：目录与可注入定义都应复用它，保证一致且字节稳定。

## 设计

### 1. `agent/client_tools.go` — 新增 2 个 helper

```go
// clientToolGroupMap 构建 server_name → 函数名集合 索引（makeExposeFn 校验用）。
// 用原始上报数据即可：buildClientToolDefs 会拒绝的非法条目（__ 分隔符/与 registry 冲突），
// 即使被暴露，filterExposedFuncDefs 也匹配不到定义，自然不注入——无害发散，不额外校验。
func clientToolGroupMap(infos []tool.ClientToolInfo) map[string]map[string]bool

// clientToolCatalog 生成客户端工具目录文本（组名+描述+函数名，无 schema）。
// 复用 buildClientToolDefs 的校验/排序，保证目录与可注入定义一致、字节稳定。
func clientToolCatalog(clientTools []ClientTool, registry *tool.Registry) string
```

catalog 输出格式（与 `registry.ToolCatalog()` 同款）：

```
客户端工具组（Mac 客户端在线时可用，同样按需启用：先调用 expose_tools.enable，启用后下一轮即可直接调用）：
- browser: Mac 客户端本地工具（browser）
  函数: browser_navigate, browser_click
```

`defs` 为空（客户端离线/全非法）返回 `""`。

### 2. `agent/orchestrator.go` — 4 处修改

**a. `mkLlmChatReq`（~line 1032-1036）** — 客户端工具改走暴露过滤 + 修正 filterToolDefs 顺序：

```go
tools = append(tools, o.exposeToolsDefinition())
exposedDefs := o.registry.GetLLMToolsFiltered(exposedFuncTools(exposed))
tools = append(tools, filterExposedFuncDefs(exposedDefs, exposed)...)
tools = append(tools, filterExposedFuncDefs(buildClientToolDefs(req.ClientTools, o.registry), exposed)...)
tools = filterToolDefs(tools, o.toolExcludes)   // 后移到客户端工具之后，让 excludes 同时覆盖
```

复用 `filterExposedFuncDefs`（暴露集 key 本就是 `组名.函数名`，客户端组/函数名与之一致，零新抽象）。
filterToolDefs 后移是必要修正：原顺序下已暴露客户端工具绕过 toolExcludes（旧顺序 append 在 filter 之后）。

**b. `makeExposeFn`（line 1636-1675）** — 认识客户端工具组：

```go
func (o *Orchestrator) makeExposeFn(state *reactState, clientGroups map[string]map[string]bool) func(...)
```

- closure 构造时：`available = sort(registry.VisibleToolNames() ∪ clientGroups keys)`
- 校验 switch 重构（保持原语义）：
  ```go
  case g == "expose_tools": reject
  case excluded[g]:         reject                    // 服务端+客户端共用
  case o.registry.Has(g) && !o.registry.IsHidden(g): 服务端原路径
  case clientGroups[g] != nil:                       客户端路径：函数名校验，写 "g.fn"
  default: reject
  ```
  原 `!registry.Has` / `IsHidden` 两分支合并进 default 拒绝，行为等价。
- 返回初始化空切片：`res := &tool.ExposeResult{Enabled: []string{}, Rejected: []string{}, Available: available}`（顺手修 `"enabled": null` 显示问题）。

**c. `executeConcurrently`（line 1679-1724）** — 循环外构建一次，闭包内复用：

```go
clientGroups := clientToolGroupMap(clientTools)
...
Expose: o.makeExposeFn(state, clientGroups)
```

**d. `Run`（line 339-348）** — Build 后追加客户端目录：

```go
if o.useNativeFC && req.Mode != model.ConvModePlan {
    if cc := clientToolCatalog(req.ClientTools, o.registry); cc != "" {
        result.Prompt += "\n" + cc
    }
}
```

## 不做的事（YAGNI）

- `loadExposure` 裸组名条目扩展客户端组：makeExposeFn 只写 `g.fn`，裸组名是死路径。
- subagent 路径（`subagent.go:334` clientWhitelist）：白名单仅 browser/ui，量小，走 MiniReAct 自有通道。
- prompt-eng 模式（doubao flash）：本就忽略客户端工具（line 1037-1038），不变。
- 客户端掉线时清理 exposedTools 中过期客户端条目：filter 自然丢弃。

## 行为变化

| 场景 | 现在 | 改后 |
|---|---|---|
| 客户端在线，首轮请求 | 全部客户端工具完整 schema 无条件注入 | 仅 expose_tools + 目录文本；browser 函数名可见、schema 无 |
| LLM 需要 browser | 直接调用 | 先 `enable([{name:"browser",functions:[...]}])`，多一轮，下一轮起可用 |
| 同窗口后续消息 | 无条件下发 | loadExposure 恢复暴露集，免重复 enable（持久化机制现有） |
| 客户端离线 | 无客户端工具 | 目录/available 均无 browser，enable 被 reject，LLM 正确告知用户 |
| plan 模式 / 收尾轮 | 无工具 | 不变 |

## 测试

1. `agent/exposure_test.go`
   - 改 `makeExposeFn(state)` 调用点（line 188、222）补第二参。
   - 新增：客户端组 enable 合法函数 → enabled；非法函数 → `browser.bogus` 入 rejected；available 含 `browser`；exclude 组 reject。
   - 新增：`mkLlmChatReq` 暴露集含 `browser.navigate` 时，browser def 只含 navigate；未暴露时无客户端 def。
2. `agent/orchestrator_final_round_test.go:224` — 更新注释；加断言：未暴露 → 无客户端 def，暴露 → 有。
3. `agent/client_tools_test.go` — 新增 catalog 用例：nil → 空串；含组名+函数名；两次调用字节一致。

## 验收

影响主链路（聊天/工具调用），按开发约定全量档：

```bash
cd server && go build ./... && go test ./internal/agent/... ./internal/tool/...
make test   # 已知测试库残留失败除外，需确认与新改动无关
```

+ 真机跑一次聊天：验证 browser 经 expose 启用后可调用，检查日志无 `[post_chat_group]` 等报错。

## 风险点

1. **LLM 可能"懒"于 enable**，客户端工具调用率下降：行为强依赖模型遵循目录指示。缓解：catalog 文案明确"先 enable 后调用"；enable 一次可多组多函数；暴露状态同窗口持久化摊薄成本。真机验收重点观察。
2. **每窗口首用多一轮 ReAct 往返**：max_iterations=8 下挤占轮次预算，task/work 长流程影响更大。缓解：持久化暴露抵消后续轮次；真机跑 work 模式任务验证收敛。
3. **客户端工具分组名与 registry 冲突**：`makeExposeFn` 中 registry 分支优先，冲突组走服务端路径不会越权；buildClientToolDefs 本会跳过冲突组，实际是"本来就不可用"，无新增风险。
4. **测试断言遗漏导致客户端工具静默消失**：`orchestrator_final_round_test.go:224` 现只断言 `len(Tools) > 0`（expose_tools 常驻即通过），必须新增"暴露后才注入"断言，否则回归静默。
5. **prompt 字节稳定性**：目录由 buildClientToolDefs 排序生成，字节稳定；客户端工具集合随连接/版本变化，system prompt 每会话本就可变，会话内重复轮次 tools 数组同集合仍稳定，不影响前缀缓存收益。
6. **持久化暴露过期条目**：客户端版本删函数后 Extra 残留 `browser.old_fn`，filter 匹配不到定义不注入，仅一点无害垃圾数据。
7. **执行路由与暴露解耦**：clientIdx 分发不依赖暴露，实现时执行层零改动即可规避"未暴露当不可执行"的误伤。