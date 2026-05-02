# P6 — 严肃思维伙伴 技术方案

> 日期: 2026-05-02 | 状态: 方案评审

---

## 一、核心目标

把 xhanHelper 打造成**冷静、中立、客观的思维伙伴**，能与人严肃讨论话题，收集并分析信息，做出逻辑推理和未来预测。

与豆包的根本区别：不谄媚、不迎合、不提供情绪价值。基于事实和逻辑说话，敢于反对用户的观点。

---

## 二、使用场景

```
用户长期关注: 房地产走势、地缘政治、宏观经济
日常行为:
  1. 收集各渠道的文章/数据 → 发给 agent（或手动描述）
  2. 与 agent 讨论 → agent 基于记忆 + 已知信息做分析
  3. agent 指出逻辑漏洞、提供反面观点
  4. agent 综合各方信息，做出预测并记录
  5. 事后验证预测 → 调整认知模型
```

---

## 三、一阶段实施项

### ⭐1 — DeepSeek 强模型接入 + 强弱切换

**当前问题**：
- 一直用 `deepseek-v4-flash`，不支持思维链
- DeepSeek 新模型（可能就是当前这个）支持思维链 + 工具调用，能力强得多

**实施内容**：

| 子项 | 说明 |
|------|------|
| 新模型接入 | 等用户发 API 文档，对照检查当前 `deepseek.go` 用法是否正确 |
| 强弱模型配置 | config.yaml 支持 `strong_model` / `weak_model` 两个模型名 |
| 自动升级规则 | 弱模型先上 → 返回 plan.execute 或 ≥3 tool_call 时自动用强模型重跑 |
| 手动触发 | `/think` 前缀直接强模型 |
| 自动降级 | 强模型对话结束后，下次消息退回弱模型 |

**架构改动**：

```
agent/orchestrator.go 新增决策层:
  1. 用户消息 → 判断是否 /think 开头
  2. 是 → ChatRequest.Model = strong
  3. 否 → ChatRequest.Model = weak
  4. 弱模型返回后，检查是否触发 "升级条件"
     - 生成了 plan.execute
     - tool_calls ≥ 3
     - 用户消息长度 > 500 字（可能是长文分析）
  5. 触发升级 → 用强模型重新调用
```

**涉及文件**：
- `internal/llm/deepseek.go` — 支持多模型、思维链解析
- `internal/llm/adapter.go` — ChatRequest 加 Model 字段
- `internal/agent/orchestrator.go` — 升级判断逻辑
- `internal/config/config.go` — 加 StrongModel / WeakModel
- `conf/config.dev.yaml` — 配置两个模型名

---

### ⭐2 — 记忆增强（向量检索）

**当前记忆系统问题**：
- 全量注入 system prompt（数据量小还行）
- 记忆提取粗糙，只有 LLM 提取摘要
- 没有语义检索，旧记忆随机会话被「遗忘」

**目标效果**：

```
用户: "我上次聊到那个关于日本房地产泡沫的观点，你再帮我分析一下"
  → embedding 搜索:"日本房地产泡沫"
  → 命中 3 条相关记忆
  → 注入 system prompt
  → LLM: "你上次提到日本泡沫有几个关键特征：...。结合你新的信息...我的分析是..."
```

**实施**：

| 子项 | 说明 |
|------|------|
| Embedding | 用 DeepSeek embedding API（如果有）或 OpenAI 兼容 embedding |
| 向量存储 | 内存 `map[memoryID][]float64`，几千条以下够用 |
| 检索 | 余弦相似度，top 5 注入 system prompt |
| 去重 | 新记忆 embedding 与旧记忆对比，相似度 > 0.9 则合并更新 |
| Prompt 注入 | `当前用户相关记忆:\n- 记忆1\n- 记忆2` 插入 system prompt |

**改动量**：约 200-300 行

**涉及文件**：
- `internal/llm/deepseek.go` — 加 Embedding() 方法（或新建 `internal/llm/embedding.go`）
- `internal/agent/memory.go` — 加向量检索方法
- `internal/agent/extractor.go` — 新记忆自动 embedding

---

### ⭐3 — P4.3b 动态图弹窗

**目标**：LLM 执行 tools 时，客户端弹出居中大弹窗，实时绘制 DAG 图。

```
用户询问 → 根节点(💬)
  ↓
LLM 返回 4 个 tool_call → 4 个 tool 节点出现，连接根节点
  ↓ 并发执行
节点状态变化: pending → running(蓝) → done(绿) / failed(红)
  ↓ 全部完成
汇总节点(📊) 出现——将工具结果发回 LLM
  ↓
LLM 继续 tool_call? → 重复上述流程
LLM 返回最终文本? → 结束节点(✅) → 弹窗淡出
```

**弹窗设计**：
- 居中弹出，半透明背景遮罩
- 可点击关闭/最小化到右上角小图标
- 节点动态生长（有动画）
- 根节点是用户询问，叶子节点是 LLM 回复

**技术方案**：
- 复用 P4.3a 的 plan 事件机制
- 客户端新增 PlanModalView

**涉及文件**：
- `client/.../Views/PlanModalView.swift` — 新增弹窗组件
- `client/.../ViewModels/ChatViewModel.swift` — 弹窗状态管理

---

### 🫱4 — Todo 工具（随手做）

简单 CRUD：添加、完成、列表、删除。Go 原生实现，零外部依赖。

---

## 四、优先级排序

| 优先级 | 模块 | 难度 | 价值 |
|--------|------|------|------|
| ⭐1 | DeepSeek 强模型 + 强弱切换 | 中 | 直接关系到「严肃讨论」的质量 |
| ⭐2 | 记忆增强（向量检索） | 中小 | 让讨论有连续性、累积性 |
| ⭐3 | 动态图弹窗 | 中大 | 炫酷体验，复杂分析时可视化 |
| 🫱4 | Todo 工具 | 小 | 顺手做 |

---

## 五、改动总览

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/llm/deepseek.go` | 改 | 支持强模型 + 思维链解析 + embedding |
| `internal/llm/adapter.go` | 改 | ChatRequest 加 Model 字段 |
| `internal/agent/orchestrator.go` | 改 | 强弱模型切换逻辑 |
| `internal/agent/memory.go` | 改 | 向量检索 |
| `internal/agent/extractor.go` | 改 | 新记忆自动 embedding |
| `internal/config/config.go` | 改 | StrongModel / WeakModel |
| `conf/config.dev.yaml` | 改 | 添加模型配置 |
| `client/.../PlanModalView.swift` | 新 | 弹窗 |
| `client/.../ChatViewModel.swift` | 改 | 弹窗状态 |
| `internal/tool/todo.go` | 新 | Todo 工具 |
