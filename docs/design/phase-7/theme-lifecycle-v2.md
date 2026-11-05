# Phase 7 — 主题生命周期重构：全自动追踪

> 日期: 2026-05-19 | 状态: 方案讨论完成，部分已开发

---

## 一、现状问题

### 1.1 主题发现重复

昨天在同一会话中，3 次后处理分别发现了「求职准备」「求职备战」「面试准备」三个语义相同的 draft，随后一起投递给用户。根因：

- `DiscoverDraftFromConversation` 没有向量去重，只靠精确名称匹配
- LLM 每次取稍有不同的名字就绕过去了
- draft 创建后不写向量，`dedupBySimilarity` 无法感知已有 draft
- 同一会话多次后处理（已通过 debounce 修复）

### 1.2 candidate 没有更新机制

candidate 一旦投递就成了死数据——用户不采纳就永远卡着，还会不断阻挡新的同类候选。7 天过期是被动的，不够。

### 1.3 用户审批模式不智能

candidate 需要用户手动采纳才变 active。但主题是「用户正在经历/关注的事情」的客观抽象——统计信息能感知，不需要用户审批确认。

---

## 二、需求

### 2.1 干掉 candidate 状态（✅ 已完成）

`candidate` 状态移除，`candidate_theme` 站内信类型移除。用户不需要「主题管理页面」。

### 2.2 全自动生命周期

```
draft（静默发现，不通知用户）
  ↓ 持续关注：向量聚类同组 ≥ N 条 AND 跨度 ≥ D 天
active（开始跟踪，生成 finding）
  ↓ 按 Duration 自动衰减
closed
  ↓ 再次被 nominate → 重新激活为 draft
```

- **draft**：从会话消息中发现，静默存储，创建时写向量
- **active**：用户持续关注的主题，LLM 生成 finding 投递用户
- **closed**：不再活跃的主题，可被重新唤醒

### 2.3 同类项合并（向量感知，下沉到 ThemeDiscoverer）

发现 draft / 升级检查 / 向量合并全部收敛到 `ThemeDiscoverer.Execute` 内部：

| 新 candidate × 已有 X | 行为 |
|-----------------------|------|
| 向量相似已有 active | 拒绝（已在跟踪） |
| 向量相似已有 draft | 放行，建新记录（保留追溯），标记 matching_draft_id |
| 向量相似已有 closed | 放行，重新激活 closed → draft，继续建新记录 |
| 无相似 | 新建 draft |

### 2.4 升级条件（向量聚类分组）

draft → active 不再按名称精确匹配，改为同一向量聚类内判断：

- 同聚类被 nominate ≥ `draft_nominate_count_threshold` 次（默认 3）
- 首次末次跨度 ≥ `draft_span_days_threshold` 天（默认 3）

### 2.5 早晚回顾替代候选投递（✅ 已完成）

原有的 `DiscoverAndDeliver` 改为 `MorningReview`（09:00）+ `EveningReview`（20:00），对 active 主题做分析生成 finding。

### 2.6 触达用户的方式

只投递 **finding**，不投递主题本身。每小时 `deliverOnce` 评审+投递 finding，保持不变。

### 2.7 active 自动衰减（新增）

`ThemeState` 加 `Duration` 字段，由 LLM 在 discover 时标注：

| Duration | 含义 | 衰减周期 |
|----------|------|----------|
| `long` | 年度关注（买房、职业规划） | 90 天无 finding → close |
| `medium` | 月度关注（项目进展、学习课程） | 30 天 |
| `short` | 一两周（旅行计划、备考冲刺） | 14 天 |
| `ephemeral` | 几天（给朋友过生日、修水管） | 7 天 |
| 未设置 | 默认 | 30 天 |

聚类时同组有多个 duration → 取最长。衰减检查在早晚回顾时执行。

### 2.8 ThemeEmbedder 小工具（新增）

复用 `ConceptEmbedService` 的「先查后存」模式：

1. 查 `ConceptEmbedding` 表（type=theme, value=name, user_id），已存在 → 跳过
2. 不存在 → 调 embedder.Embed(name) → 写 vectorStore（metadata: type=theme）+ embedding 表 + concept 表

`ThemeTool.create_theme` 和 `ThemeDiscoverer.Execute` 都调它。

---

## 三、架构收敛

### 收敛前

```
DiscoverDraftFromConversation (ReflectionOrchestrator)
  → ThemeDiscoverer.Execute (LLM 发现 + dedupBySimilarity)
  → 名称查重 (GetByName)
  → 创建 draft
  → checkDraftUpgrade (按名称分组)
```

### 收敛后

```
DiscoverDraftFromConversation (ReflectionOrchestrator，薄壳)
  → 取消息 → ThemeDiscoverer.Execute("discover", {mode:"conversation", messages})
    → LLM 发现 candidates
    → dedupBySimilarity v2（向量搜索已有 draft/active/closed，按状态分流）
    → ThemeEmbedder（写向量）
    → 创建/更新 draft
    → checkDraftUpgrade（向量聚类分组）
  → 返回 {created, upgraded, rejected}
```

`ReflectionOrchestrator` 不再需要 embedder / vectorStore / embeddingRepo。

---

## 四、改动清单

### 4.1 Model 层

**`model/theme_state.go`**（✅ 已完成）：
- `Status` 枚举：`draft` / `active` / `closed`
- 新增 `Duration` 字段：`long` / `medium` / `short` / `ephemeral`

### 4.2 Repo 层

**`service/theme_state_repo.go`**（✅ 已完成）：
- `ListCandidates` / `List*ByUser` → 复用 `ListByStatus`
- `ExpireCandidates` → `ExpireDrafts`（只过期 draft）
- `Reactivate` → `ReactivateDraft`（closed → draft）
- 新增 `Stats` / `GlobalStats`（监控用）

**新增 active 衰减相关**：
- `ListActiveExpired(ctx, userID)` — 查该衰减的 active
- `ExpireActives(ctx, userID)` — 过期 active → closed

### 4.3 Agent 层

**`agent/theme_discover.go`**（核心改动）：
- `dedupBySimilarity` v2：对 active → 拒绝；draft → 放行+标记；closed → 重新激活
- `Execute` 内部收敛 draft 创建 + 升级检查
- 调 `ThemeEmbedder` 写向量

**`agent/reflection.go`**（✅ 已完成）：
- `DiscoverDraftFromConversation` → 薄壳，只调 ThemeDiscoverer
- `deliverCandidate` / `rewriteCandidate` → 删除
- `DiscoverAndDeliver` → `MorningReview` + `EveningReview`
- `checkDraftUpgrade` 下沉到 ThemeDiscoverer
- 移除 embedder/vectorStore/embeddingRepo 依赖

**`agent/theme_embedder.go`**（新增）：
- 包装 `ConceptEmbedService`，传 `conceptType="theme"`
- vectorStore metadata 用 `type: "theme"`

**`agent/cron_runner.go`**（✅ 已完成）：
- `dispatchPostChat` 重新启用 draft 发现
- `discoverOnce` → 早晚回顾
- 早晚回顾中增加 active 衰减检查

### 4.4 Tool 层

**`tool/theme.go`**（✅ 已完成）：
- `create_theme` 直接建 active，调 `ThemeEmbedder` 写向量

### 4.5 配置

**`conf/config.yaml`**（`reflection` 段）：

```yaml
reflection:
  draft_nominate_count_threshold: 3
  draft_span_days_threshold: 3
  draft_expire_days: 7
  active_duration:
    long: 90
    medium: 30
    short: 14
    ephemeral: 7
```

### 4.6 前端 / 信箱（✅ 已完成）

- `candidate_theme` 信箱项类型移除
- 信箱 service 中 `buildCandidateTags` 删除
- mailbox 工具 schema 中去掉 `candidate_theme` 枚举值

### 4.7 可观测性（✅ 已完成）

- 监控看板新增主题追踪卡片
- `SnapshotProvider.Themes()` 提供 `draft/active/closed` 计数 + 今日活动

---

## 五、边界情况

| 场景 | 预期行为 |
|------|----------|
| 同一会话多次 nominate 同一主题 | 每次建新 draft（保留追溯） |
| draft 刚升级 active 后，又发现相似 draft | dedupBySimilarity 拒绝（active 向量已存在） |
| closed 主题被重新 nominate | dedupBySimilarity 放行 + 重新激活为 draft |
| 多个 draft 都满足升级条件 | 选最新一条升 active，其余同聚类 draft close |
| 同聚类有不同 Duration | 取最长 |
| 无历史 session 的新用户 | 无 draft，无 active，等待发现 |

---

## 后续变更

- 2026-11-05：每日主题回顾（`MorningReview` / `EveningReview`）已停用，`CronRunner` 不再按 `daily_schedules` 触发；主题发现/分析/归档整条链路不再运行。`theme` 管理工具、`get_topic_stats` / `get_topic_detail` / `get_bill_stats` 已删除；`agent/theme_*`、`agent/state_*` 作为未接线参考代码保留（prompt 中的旧工具名已失效，重新接线前需先恢复工具）。影响文件：`server/internal/agent/cron_runner.go`、`server/internal/agent/reflection.go`、`server/internal/tool/`、`server/internal/appinit/`、`server/internal/config/config.go`。
