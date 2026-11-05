# Phase 15: 主题感知层 v2 —— 实时更新 + 更准检索

## 1. 背景

Phase 13 完成主题系统重构（去 draft、记忆驱动 StateUpdater、按需 ThemeAnalyst）。但遗留两个核心问题：

### 1.1 检索不精准

当前 `ThemeSource.Fetch()` 只用单条用户消息 embedding 做向量搜索。用户聊求职但说「好的，我试试看」时，向量匹配不到「求职准备」主题。实测数据：默认对话最近 20 条用户消息全是求职话题，但单句模糊消息常失联。

### 1.2 更新不及时

更新路径 `对话 → NotifyChatEnd → debounce(30min) → DelayedTask → MemoryExtractor(有损) → StateUpdater(topic 脆弱匹配)` 有三个断点：debounce 延迟、Memory 提取有损、topic 名不匹配。实际 4 个 active 主题中 2 个 state_text 为 0 字，273 条 post_chat 任务全完成但 state 更新远慢于聊天进度。

## 2. 目标

1. **检索**：多历史消息 + 主题事实 chunk 双源搜索
2. **更新**：LLM 在对话中通过 `append_theme_state` tool call 实时追加事实
3. **system prompt 注入**：命中主题注入摘要 + 最新 facts，LLM 无需反复调 get_theme_state

## 3. 数据模型

### 3.1 theme_states 表新增

```sql
ALTER TABLE theme_states ADD COLUMN state_facts TEXT;
```

| 字段 | 类型 | 角色 | 修改方式 |
|------|------|------|----------|
| `state_text` | TEXT | LLM 阅读的叙事摘要（≤500字） | 异步从 facts 生成 |
| `state_facts` | TEXT | 向量搜索的原子事实（JSONL） | LLM tool call 增量追加 |

`history_state_text` 迁移后不再使用。

### 3.2 JSONL 格式

```
{"ts":"2026-06-02","fact":"面试赤子城，AIGC小游戏方向，感觉不错"}
{"ts":"2026-06-10","fact":"百度面试无后续，可能已凉"}
{"ts":"2026-06-10","fact":"赤子城HR电话，后续待定"}
```

- `ts`: 事实发生或记录的日期
- `fact`: 1-2 句自包含事实陈述
- **只追加，不修改，不删除**

### 3.3 ConversationExtra（不改动）

不新增 `PinnedThemeIDs` 等粘滞字段。默认对话的消息历史天然提供上下文窗口，不需要额外粘滞机制。

## 4. 工具改造

### 4.1 ThemeTool 新增 function

| function | 输入 | 输出 | 说明 |
|----------|------|------|------|
| `get_theme_state` | `name` | `{name, description, state_text, state_facts}` | 获取主题完整状态 |
| `append_theme_state` | `name, fact` | `{status, fact_index}` | 追加一条事实 |
| `list_active_themes` | 无 | `[{name, description, status}]` | 列出所有 active 主题 |

### 4.2 工具描述

```
get_theme_state:
  "获取主题完整状态（摘要+全部事实列表）。建议在追加事实前调用，了解已有记录避免重复。若主题不存在，返回当前所有 active 主题列表"

append_theme_state:
  "向主题追加一条新进展事实。每次只追加一条（1-2句话）。适用场景：进度更新、状态变化、新发现。日常闲聊不调用"

list_active_themes:
  "列出所有活跃追踪主题。不确定有哪些主题时调用"
```

### 4.3 get_theme_state 找不到时的兜底

```
get_theme_state("不存在主题")
  → error: "主题「不存在主题」未找到。当前 active 主题：求职准备、睡眠追踪、职业与生活、个人助手。"
```

### 4.4 对话中 LLM 工作流

```
system prompt 里看到:
  ### 当前关注
  **求职准备** — 追踪简历修改、面试准备...
  当前状态：求职目标 Kira Learning...
  最近更新：[- 06-10 百度面试无后续 - 06-10 赤子城HR电话 ...]

  → LLM 知道新进展 "06-11 接到字节HR电话" 不在列表中
  → append_theme_state("求职准备", "06-11 接到字节HR电话，约下周一面试")
  → 无需先调 get_theme_state（摘要+最新facts已在prompt里）
```

## 5. 异步摘要重生

每次 `append_theme_state` 触发后台 goroutine：

```
1. 读旧 state_text + 全部 state_facts
2. 调 flash LLM 生成新摘要
3. 写回 state_text
```

### 5.1 摘要 prompt

```
你是主题状态摘要员。基于旧摘要和最新事实时间线，生成不超过500字的叙事摘要。

## 旧摘要
{oldSummary}

## 事实时间线
- [06-02] 面试赤子城，AIGC小游戏方向，感觉不错
- [06-10] 百度面试无后续，可能已凉
- [06-10] 赤子城HR电话，后续待定

## 规则
1. 保留旧摘要中与新事实不冲突的所有有效信息
2. 过期或被新事实覆盖的信息用新内容替换
3. 每条关键信息标注大致时间
4. 超过3个月且未再出现的旧信息可移除
5. 不超过500字

## 输出
{"summary": "..."}
```

## 6. Chunk 嵌入

### 6.1 增量策略

facts 只追加不修改 → chunk 只增不删。每次 `append_theme_state` 追加一条 fact 后，异步对**该条新 fact**建 chunk。不清除旧 chunk。

### 6.2 Chunk 内容

嵌入文本 = `fact` 字段内容（不含 ts 和 JSON 包装）。

VectorStore metadata：
```
{
    "type": "chunk",
    "parent_type": "theme",
    "parent_id": themeID,
    "user_id": userID
}
```

## 7. 检索增强

### 7.1 ThemeSource.Fetch() 三路合并

```
输入: SearchOptions{
    Embedding:           curMsgVec,
    HistoryEmbeddings:   [prev1Vec, prev2Vec, prev3Vec, prev4Vec, prev5Vec],
}

搜索:
  路1: curMsgVec → VectorStore.Search(type=theme)           (现有)
  路2: prev1Vec..prev5Vec → VectorStore.Search(type=theme)  (新增)
  路3: curMsgVec → VectorStore.Search(type=chunk && parent_type=theme)  (新增)

合并: 每个主题取三路最高分 → 过 threshold → 入选
```

### 7.2 改动点

| 文件 | 改动 |
|------|------|
| `retrieval/types.go` | `SearchOptions` 加 `HistoryEmbeddings [][]float64` |
| `retrieval/theme_source.go` | 三路搜索 + 聚合 |
| `promptbuilder/types.go` | `BuildOptions` 加 `HistoryEmbeddings` |
| `agent/orchestrator.go` | `mkPromptBuildOpt` 取最近 5 条 user msg embedding |

历史 embedding 从消息表已有字段复用，不重新嵌入。

## 8. System Prompt 注入增强

### 8.1 命中的主题

```
### 当前关注

**求职准备** — 追踪简历修改、面试准备、投递计划等
当前状态：求职目标 Kira Learning。06-02 面试赤子城...
最近更新：
  - [06-10] 百度面试无后续
  - [06-10] 赤子城HR电话，后续待定
  - [06-02] 面试赤子城，AIGC小游戏方向
```

三段：name + description → 摘要 → 最新 3 facts。

### 8.2 未命中的主题

仅注名字：`**睡眠追踪**（未匹配当前对话）`。不注摘要避免噪音。

### 8.3 实现

改 `promptbuilder/providers.go` 的 `ThemeProvider.Search()`，从 `ContextPacket` 读 metadata 拼 Text。`ThemeSource.Fetch()` 返回的 `ContextPacket` 带上 `state_text` 和最新 `facts`。

## 9. 历史数据迁移

### 9.1 现状

history_state_text 格式规整：

```
--- 2026-05-19 20:53 ---
...
--- 2026-05-21 09:01 ---
1. [2026-05-19] 用户完成简历制作，已投递多家公司
2. [2026-05-19] 发现简历中"工具热注册"实现不牢
```

4 个 active 主题各 18k~27k 字符。

### 9.2 迁移脚本

服务启动时一次性执行：

```
for each active theme:
  1. 若 state_facts 非空 → skip
  2. 若 history_state_text 为空 → state_facts = ""，skip
  3. 规则解析:
     a. split by /--- \d{4}-\d{2}-\d{2}.*---/
     b. 匹配 /^\d+\.\s*\[(\d{4}-\d{2}-\d{2})\]\s*(.+)/
     c. 产出 {"ts":"...","fact":"..."}
  4. 若产出 < 3 条 → LLM 兜底解析
  5. 写入 state_facts
  6. BuildChunks(全部 facts 拼接)
  7. RegenerateSummary(oldStateText, facts)
```

## 10. 删除项

### 10.1 StateUpdater — 删除

理由：
- 它的功能已被 `append_theme_state` tool call 替代
- 它的 LLM 直接写 state_text 行为与新模型冲突（state_text 应由异步摘要生成器维护）
- Memory → topic 匹配 → state 更新的异步路径太慢太脆弱

### 10.2 ThemeAnalyst 的状态更新能力 — 移除

`dailyReview()` 改为只做：
```
expireActiveThemes()  →  关闭过期主题
ThemeDiscoverer.run()  →  从 topic/note/memory 聚类发现新主题
```

ThemeAnalyst 类保留以备后用，但不再在调度中调用。

### 10.3 受影响的调度

| 文件 | 改动 |
|------|------|
| `state_updater.go` | **删除** |
| `cron_runner.go` | `dispatchPostChat` 中删 StateUpdater 调用 |
| `reflection.go` | `dailyReview` 中 ThemeAnalyst.analyze 替换为 ThemeDiscoverer run |
| `appinit/agents.go` | 删 StateUpdater 初始化 |

## 11. 保留项

- `ThemeDiscoverer` — 从 topic 聚类发现新主题
- `post_chat` 延迟任务 — Memory 提取仍有用（长期记忆归档）
- `cron_runner.go` — 调度框架不变
- `get_topic_detail` / `get_topic_stats` 工具
- Mailbox / Reflection 管线
- 前端 / 客户端

## 12. 文件清单

### 新增

| 文件 | 职责 |
|------|------|
| `agent/theme_summary.go` | `RegenerateSummary()` 异步生成摘要 |
| `agent/theme_chunker.go` | 增量 BuildChunks 单条 fact |
| `agent/theme_migration.go` | 启动时历史迁移 |
| `migrations/add_state_facts.sql` | DDL |

### 修改

| 文件 | 改动 |
|------|------|
| `model/theme_state.go` | + `StateFacts string` |
| `service/theme_state_repo.go` | + `AppendFact()` / `GetFacts()` / `UpdateSummary()` |
| `tool/theme.go` | + 3 个 function |
| `retrieval/types.go` | `SearchOptions` + `HistoryEmbeddings` |
| `retrieval/theme_source.go` | 三路搜索 + `ContextPacket` 带 state_text/facts |
| `promptbuilder/types.go` | `BuildOptions` + `HistoryEmbeddings` |
| `promptbuilder/providers.go` | `ThemeProvider` 注入摘要 + 最新 facts |
| `agent/orchestrator.go` | `mkPromptBuildOpt` 传历史 embedding |

### 删除

| 文件 | 原因 |
|------|------|
| `agent/state_updater.go` | tool call 替代 |

### 连带修改

| 文件 | 改动 |
|------|------|
| `agent/cron_runner.go` | 删 StateUpdater 调用 |
| `agent/reflection.go` | `dailyReview` 改调度 |
| `appinit/agents.go` | 删 StateUpdater 初始化 |

## 13. 执行顺序

```
1. Migration（DDL + 迁移脚本）
2. Service 层（AppendFact / GetFacts / UpdateSummary）
3. Tool 层（get_theme_state / append_theme_state / list_active_themes）
4. Agent 层（RegenerateSummary / ThemeChunker / ThemeMigration）
5. 删除项（StateUpdater 及调用）
6. Retrieval 层（多历史 + chunk 搜索 + ContextPacket 增强）
7. System Prompt 注入增强
8. Orchestrator 层（传 HistoryEmbeddings）
```

每步独立可测。

## 14. 边界情况

| 场景 | 处理 |
|------|------|
| LLM 猜错主题名 | `get_theme_state` 返回 error + 所有 active 名称 |
| facts 为空（新主题） | 摘要从 name+desc 生成初始叙事 |
| 摘要生成失败 | 静默降级，state_text 保持旧值 |
| chunk 嵌入失败 | 独立 goroutine，不影响主流程 |
| 旧主题无 facts | 迁移脚本填充，或冷启动从零开始 |

## 15. 取舍记录

| 取舍 | 理由 |
|------|------|
| 不做粘滞主题 | 默认对话消息历史天然提供粘滞 |
| facts 只追加不修改 | 保证 chunk 永久有效 |
| 不删除旧 chunk | facts 不可变 |
| 摘要用 flash 异步生成 | 不要求实时 |
| 删除 StateUpdater | tool call 完全替代，避免双写冲突 |
| ThemeAnalyst 不再更新状态 | 状态更新由 LLM 在对话中完成 |
| ThemeDiscoverer 保留 | 新主题发现仍需离线聚类 |
| system prompt 注摘要 + 最新 facts | LLM 无需反复调 get_theme_state |

---

## 16. 实施拆分

6 个 Part，每个 2-4 个文件，独立可测。

### Part 1: 数据基础（migration + model + service）

**无行为变更**，纯加字段 + 历史迁移 + 读写方法。

| 文件 | 动作 |
|------|------|
| `migrations/add_state_facts.sql` | 新增 | DDL |
| `model/theme_state.go` | 修改 | + `StateFacts string` |
| `agent/theme_migration.go` | 新增 | 启动时规则解析 history → JSONL，建 chunk + 重生摘要 |
| `service/theme_state_repo.go` | 修改 | + `AppendFact()` / `GetFacts()` / `UpdateSummary()` |

**验证**：启动后 4 个主题 state_facts 非空，chunk 写入 VectorStore。

---

### Part 2: 工具函数（tool call 能力）

LLM 可在对话中调这三个函数。

| 文件 | 动作 |
|------|------|
| `tool/theme.go` | 修改 | + `get_theme_state` / `append_theme_state` / `list_active_themes` |

**验证**：API 测试脚本直接调三个函数，检查返回。

---

### Part 3: 异步摘要 + Chunker

`append_theme_state` 的后台处理。

| 文件 | 动作 |
|------|------|
| `agent/theme_summary.go` | 新增 | `RegenerateSummary()` 从 facts 生成摘要 |
| `agent/theme_chunker.go` | 新增 | 增量 `BuildChunks` 单条 fact |

在 `tool/theme.go` 的 `append_theme_state` 里加 `go func()` 触发。

**验证**：调 `append_theme_state` 后等 2 秒，`get_theme_state` 返回的新 state_text 包含新事实。

---

### Part 4: 清理旧代码

删除被替代的逻辑。

| 文件 | 动作 |
|------|------|
| `agent/state_updater.go` | 删除 |
| `agent/cron_runner.go` | 修改 | `dispatchPostChat` 删 StateUpdater 调用 |
| `agent/reflection.go` | 修改 | `dailyReview` 中 ThemeAnalyst.analyze 替换为 ThemeDiscoverer run |
| `appinit/agents.go` | 修改 | 删 StateUpdater 初始化 |

**验证**：编译通过，服务启动无报错。

---

### Part 5: 检索增强

多历史 + chunk 双源搜索。

| 文件 | 动作 |
|------|------|
| `retrieval/types.go` | 修改 | `SearchOptions` + `HistoryEmbeddings` |
| `retrieval/theme_source.go` | 修改 | 三路搜索 + `ContextPacket` 带 `StateText` / `LatestFacts` |

**验证**：重放对比工具（已有）可测新旧检索结果差异。

---

### Part 6: System Prompt + Orchestrator

注入增强 + 历史 embedding 传递。

| 文件 | 动作 |
|------|------|
| `promptbuilder/types.go` | 修改 | `BuildOptions` + `HistoryEmbeddings` |
| `promptbuilder/providers.go` | 修改 | `ThemeProvider` 注入摘要 + 最新 3 facts |
| `agent/orchestrator.go` | 修改 | `mkPromptBuildOpt` 取最近 5 条 user msg embedding |

**验证**：实际对话，system prompt 里看到完整主题摘要 + facts。

---

### 依赖关系

```
Part 1 → Part 2 → Part 3
                  ↘
                    Part 4（可在 2 完成后做）
Part 1 ──────────────────→ Part 5 → Part 6
```

Part 4 独立，可在 Part 2+3 验证通过后随时做。Part 5 依赖 Part 1 的 facts 和 chunk 已存在。Part 6 依赖 Part 5 的 `ContextPacket` 带 state/facts。

---

## 后续变更

- **2026-11-05：主题/状态子系统整体删除。** 本设计描述的 `ThemeAnalyst` / `ThemeDiscoverer` / `StateUpdater` / `ThemeStore` / `ThemeEmbedder` / `theme_states` 表与 `reflection` 配置段全部移除（事件系统已接管该职责）。存量数据的 `theme_states` 行与 theme 向量用 `server/cmd/entity-gc --purge-legacy-themes --write` 清理。本文档仅作历史记录，不再对应代码。
