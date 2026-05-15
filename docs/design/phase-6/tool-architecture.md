# Phase 6 — 工具体系架构

> 日期: 2026-05-15 | 说明: 反思 Agent 引入的新工具与现有工具体系的关系

---

## 一、Registry 分层

反思系统引入后，工具按使用场景分属三个 Registry，互不污染：

```
主 Orchestrator（聊天场景）
  └── 全局 Registry（现有）
        bookkeeping, datetime, note, calculator, uuid, agent, markdown, tool_retriever
        动态工具（每 30s 从 DB 加载）

ReflectionOrchestrator（反思场景）
  └── Reflection Registry（新建）
        ThemeAnalyst, Scorer, MailboxTool, ThemeTool
        （不包含聊天工具，反思 LLM 看不到记账/日期等）

ThemeAnalyst 内部 mini ReAct
  └── Retrieval Registry（新建, 共享）
        search_messages, search_notes, search_memories, get_bill_stats

ThemeDiscoverer 内部 mini ReAct
  └── Retrieval Registry（同上, 共享）
        search_messages, search_notes, search_memories
```

## 二、工具分类

### 内部 LLM 工具

封装 LLM ChatSync 调用，实现 `tool.Tool` 接口。被 ReflectionOrchestrator 的 ReAct 循环作为工具调用。

| 工具 | 内部 LLM 调用 | 说明 |
|------|-------------|------|
| ThemeAnalyst | 是（1-3 轮） | 分析单个主题，产出发现 + 更新状态页 |
| Scorer | 是（1 轮） | 对发现打分排序，返回 TopN |
| ThemeDiscoverer | 是（1-2 轮检索） | 发掘候选主题 |

### 检索工具

包装现有 VectorStore + Repo 查询，不调 LLM，实现 `tool.Tool` 接口。

| 工具 | 封装的底层能力 | 说明 |
|------|--------------|------|
| search_messages | VectorStore.Search + MessageRepo | 向量检索 user role 消息 |
| search_notes | VectorStore.Search + NoteRepo | 向量检索笔记 |
| search_memories | VectorStore.Search + UserMemoryRepo | 向量检索长期记忆 |
| get_bill_stats | BillRepo 统计方法 | 结构化消费统计摘要 |

### 工程工具

纯 DB 操作，不调 LLM。

| 工具 | 说明 |
|------|------|
| MailboxTool | 投递消息到信箱（mailbox_deliver） |
| ThemeTool | 创建/关闭/重新激活主题 |

## 三、关键设计决策

### 为什么分三个 Registry 而不是一个

- **Token 成本**：工具定义注入 system prompt，聊天场景不需要 10 个反思工具的定义
- **LLM 认知负载**：可用的工具越多，LLM 选错工具的概率越大
- **安全性**：反思 LLM 不应有记账/删除笔记等操作权限

### 工具集的注入方式

公共 ReAct 组件通过参数接受工具集：

```go
type ReActLoop struct {
    client     llm.LLMClient
    tools      map[string]tool.Tool  // 本轮可用的工具
    systemPrompt string
    maxIterations int
}
```

不同场景注入不同工具集：
- 聊天：主 Registry 的所有工具
- 反思主循环：Reflection Registry（ThemeAnalyst, Scorer, MailboxTool, ThemeTool）
- ThemeAnalyst 内部：Retrieval Registry
- ThemeDiscoverer 内部：Retrieval Registry

### 检索工具是否开放给聊天场景

暂不开放。未来可以考虑在对话中让用户手动检索自己的历史消息，但 MVP 阶段检索工具仅服务于反思系统。

## 四、实现清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/agent/react_loop.go` | 新 | 公共 ReAct 循环组件 |
| `internal/agent/registry.go` | 改或新 | 支持多个 Registry 实例（或复用现有 Registry 的多实例能力） |
| `internal/tool/search_messages.go` | 新 | search_messages 检索工具 |
| `internal/tool/search_notes.go` | 新 | search_notes 检索工具 |
| `internal/tool/search_memories.go` | 新 | search_memories 检索工具 |
| `internal/tool/get_bill_stats.go` | 新 | get_bill_stats 检索工具 |
| `internal/tool/mailbox.go` | 新 | MailboxTool |
| `internal/tool/theme.go` | 新 | ThemeTool |
