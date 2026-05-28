# registry 输出规范职责越权

## 问题

`tool/registry.go` 目前输出了四类内容，但并非全部属于工具注册表的职责：

| registry 输出 | 实际归属 |
|---|---|
| 工具定义（内联 + 概览） | registry ✓ |
| 输出格式（JSON 样例） | orchestrator（`parseLLMResponse` 需要这个格式） |
| 输出规范（JSON 强制、大胆用工具、search 前置、reply 为空） | orchestrator/promptbuilder（规则约束的是 LLM → orchestrator 交互协议） |
| tool_retriever 使用规则 | registry（决定工具调用模型） |

## 影响

- `GenerateImportantRules()` 输出的规范本质上是 orchestrator 层的约束，却由 registry 生成
- `writePromptFooter()` 的输出格式说明嵌在工具定义里，格式变更要动 registry 而不是 orchestrator
- promptbuilder 作为 system prompt 的唯一构建入口，部分内容却不由它控制

## 建议方案

registry 只输出工具定义（内联 + 概览 + search 规则），输出规范和 JSON 格式说明移到 promptbuilder 的 static provider 或 orchestrator 层。

## 优先级

低。当前这块内容不频繁变更，架构问题暂不阻塞功能开发。
