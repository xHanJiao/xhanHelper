# Theme 系统重构

## 当前状态

### 数据（2026-06-06，单用户）

| 指标 | 值 |
|------|-----|
| 总主题数 | 25 |
| active | 4 |
| draft | 3 |
| closed | 18 |
| 日均新增 | 1-2 个 |
| 日均用户消息 | 40-50 条 |

### 生命周期

```
发现 → draft → (3次提名+3天跨度) → active → (无活动后衰减关闭)
                ↑ 3个draft被卡在这里
```

**创建**：ThemeDiscoverer 在 dispatchPostChat 和早晚回顾中从对话里提名候选主题，创为 draft

**升级**：`dedupBySimilarity` 通过向量去重 + 聚类计数，同一 direction 被提名 ≥3 次、跨度 ≥3 天，最早 draft 升 active

**更新**：
- StateUpdater：仅当会话**绑定了 ThemeID** 时触发，增量更新状态页
- ThemeAnalyst：早晚各一次，对**所有** active 主题跑 ReAct 深度分析，产出 findings → Scorer → Secretary → Mailbox 投递

**关闭**：draft 7 天过期；active 按 duration 衰减（ephemeral=7d, short=14d, medium=30d, long=90d）

### 问题

1. **升级门槛过严**：3 个 draft 被卡住，active 只有 4 个。系统对用户世界的建模粒度太粗
2. **状态更新覆盖太窄**：StateUpdater 只在会话绑定 ThemeID 时触发，90% 的日常聊天不走这个路径。主题状态页沦为创建时的 snapshot
3. **全量分析浪费**：ThemeAnalyst 不管有无新活动都对所有 active 主题跑，大部分是重复分析
4. **Theme 在投递垃圾**：产出的 findings 本质是复述用户告诉它的信息，不是真正的洞察。通知应该是 Mission/Signal 的事

---

## 改什么

### 理念

**Theme = 认知层**：对用户世界的结构化理解。不通知用户，只在对话中提供认知深度。分析归 Theme，通知归 Mission/Signal。

### 变更清单

**创建**：

1. **去掉 draft，直接 active**
   - `dedupBySimilarity` 从 6 分支简化为 3 分支：
     - 撞 active → reject
     - 撞 closed → reactivate 为 active（保留旧 state_text）
     - 无匹配 → 创建 active
   - 删除聚类升级（`nominateCountThreshold` / `spanDaysThreshold`）、`merge_draft`、`checkDraftUpgrade`

2. **创建时写初始状态页**：ThemeDiscoverer 提名时 LLM 产出的 rationale 直接写为 state_text

3. **prompt 改语义**：从「需要长期追踪价值」→「用户生活中有意义的方面。一次性琐事不算」

**更新**：

4. **StateUpdater 改为记忆驱动**
   - 新增 `UpdateFromMemory(themeID, memory)`，输入为 MemoryExtractor 产出的记忆
   - dispatchPostChat 中用记忆做多路召回匹配活跃主题：
     - 向量路：记忆 embedding → VectorStore.Search(type="theme")
     - Topic 路：记忆 topic 精确/LIKE 匹配主题名称
     - Tag 路：记忆 tags → ConceptEmbedding → 搜主题概念向量
     - 三路合并排序，过阈值触发 StateUpdater
   - 一条记忆命中多个主题 → 分别独立更新

5. **删除 `UpdateFromConversation`**：连带 `Conversation.ThemeStateCursor` 字段

6. **MemoryExtractor 嵌入同步化**：`storeMemory` 中异步 goroutine → 同步，确保向量在 StateUpdater 前就绪

**分析**：

7. **ThemeAnalyst 按需分析**：`reviewActiveThemes` 加 `UpdatedAt > LastRunAt` 判断

8. **ThemeAnalyst 不再产出 finding**：只输出 updated_state 和 summary

**关闭**：

9. **Active 衰减保留**：long=90d, medium=30d, short=14d, ephemeral=7d

10. **删除 draft 过期**：`themeStore.Expire()` 和调用处

**其它**：

11. **配置清理**：删除 `draft_nominate_count_threshold` / `draft_span_days_threshold` / `draft_expire_days`

12. **DB 迁移**：现有 3 条 draft → active

13. **重放独立主题阈值**：全链路加 `ThemeThreshold` / `ThemeLimit`，和记忆/笔记/历史平权

14. **前端清理**：删除 candidate/采纳/忽略 UI

---

## 讨论要点

### 为什么 draft 可以去掉

数据验证：72% 的主题已经 close，自动衰减机制有效。去掉门槛后稳态活跃主题预计 10-15 个，2-3 倍成本，单用户场景可控。

### 为什么 StateUpdater 要改记忆驱动

旧的 cursor 机制有硬伤：同一对话命中多个主题时，cursor 在第一个主题更新后前移，后续主题读到 0 条新消息。记忆驱动后，记忆本身就是输入，不依赖 cursor。

另外，用 LLM 精炼过的记忆做匹配比 20 条原始消息拼接成向量质量高得多。

### 为什么 ThemeAnalyst 不再产 finding

Theme 的新定位是纯认知层。finding 的投递走 Score r→Secretary→Mailbox 管线，但这些 finding 本质是复述用户已知信息，不是洞察。真正的洞察应该来自 Mission（用户托付的追踪）和 Signal（系统自主判断的干预时机）。

此项改动后 Scorer/Secretary 暂时保留（后续 Phase 处理）。

### 搜索信噪比

主题变多后 ThemeSource 在一段对话中可能返回更多候选。通过重放支持调整阈值后可以实验找到最优值。阈值是关键调控点。

---

## 分批实施

| 批次 | 范围 | 关键文件 |
|------|------|---------|
| **1** | dedupBySimilarity 简化 + prompt + 初始状态页 | `theme_discover.go` |
| **2** | StateUpdater 记忆驱动 + MemoryExtractor 同步嵌入 | `state_updater.go`, `extractor.go`, `cron_runner.go` |
| **3** | ThemeAnalyst 按需分析 + 不产 finding | `theme_analyst.go`, `reflection.go` |
| **4** | 删除 dead code + 配置清理 + 前端清理 | `reflection.go`, `cron_runner.go`, `theme_discover.go`, `theme_store.go`, `model/conversation.go`, `config.go`, `config.*.yaml`, `ThemeView.vue` |
| **5** | 重放主题阈值 + DB 迁移 | `retrieval/types.go`, `promptbuilder/*`, `orchestrator.go`, `replay/*`, `replay_handler.go`, `ReplayCompareView.vue` |
| **6** | StateUpdater + ThemeAnalyst 开放 search_history | `state_updater.go`, `theme_analyst.go`, `appinit/tools.go` |
