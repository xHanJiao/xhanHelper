# Phase 7 — 主题生命周期重构：全自动追踪

> 日期: 2026-05-19 | 状态: 方案讨论完成，待开发

---

## 一、现状问题

### 1.1 主题发现重复

昨天在同一会话中，3 次后处理分别发现了「求职准备」「求职备战」「面试准备」三个语义相同的 draft，随后一起投递给用户。根因：

- `DiscoverDraftFromConversation` 没有向量去重，只靠精确名称匹配
- LLM 每次取稍有不同的名字就绕过去了
- 同一会话多次后处理（已通过 debounce 修复）

### 1.2 candidate 没有更新机制

candidate 一旦投递就成了死数据——用户不采纳就永远卡着，还会不断阻挡新的同类候选。7 天过期是被动的，不够。

### 1.3 用户审批模式不智能

candidate 需要用户手动采纳才变 active。但主题是「用户正在经历/关注的事情」的客观抽象——统计信息能感知，不需要用户审批确认。让用户管理主题违背了 Phase 7 vision-v2 的「数字镜子」定位。

---

## 二、需求

### 2.1 干掉 candidate 状态

主题不再是需要用户审批的提案。`candidate` 状态移除，`candidate_theme` 站内信类型移除。用户不需要「主题管理页面」。

### 2.2 全自动生命周期

```
draft（静默发现，不通知用户）
  ↓ 持续关注：被 nominate ≥ N 次 AND 首次末次间隔 ≥ D 天
active（开始跟踪，生成 finding）
  ↓ 不再聊，自然衰减
closed
  ↓ 再次被 nominate → 重新激活为 draft
```

- **draft**：从会话消息中发现，静默存储，不投递站内信
- **active**：用户持续关注的主题，LLM 生成 finding 投递用户
- **closed**：不再活跃的主题，可被重新唤醒

### 2.3 同类项合并（向量感知）

相同语义的主题不创建多条记录。发现新 draft 时，用向量相似度（≥ 0.70）匹配已有主题：

| 新 draft × 已有 X | 行为 |
|-------------------|------|
| 已有 draft | 新建 draft，旧的不动（保留追溯记录） |
| 已有 active | 丢弃 draft（已在跟踪，可选更新元数据） |
| 已有 closed | 重新激活为 draft，`status = draft`，重置 nominate 计数 |

### 2.4 升级条件（配置化）

draft → active：**被 nominate ≥ `draft_nominate_count_threshold` 次 AND 首次 nominate 到末次 nominate 跨度 ≥ `draft_span_days_threshold` 天**。

- `draft_nominate_count_threshold` 默认 3
- `draft_span_days_threshold` 默认 3

同一主题被 nominate 的次数 = 有多少条向量相似的 draft 记录（不含已 close 的）。跨度 = 最早和最晚 draft 记录的 `created_at` 之差。

### 2.5 早晚回顾替代候选投递

原有的 `DiscoverAndDeliver`（09:00/20:00 发现候选+投递）改为早晚回顾：

| 时间 | 语义 | 问题 | 输出 |
|------|------|------|------|
| 09:00 早安回顾 | 开始新一天 | 「今天可能要注意什么？」 | finding → PendingFinding |
| 20:00 晚安回顾 | 结束一天 | 「今天发生了什么变化？」 | finding → PendingFinding |

### 2.6 触达用户的方式

只投递 **finding**，不投递主题本身。每小时 `deliverOnce`（`DeliverPendingFindings`）评审+投递 finding，保持不变。

---

## 三、改动清单

### 3.1 Model 层

**`model/theme_state.go`**：
- `Status` 枚举去掉 `candidate`，保留 `draft` / `active` / `closed`（`archived` 也干掉，和 `closed` 合并）

### 3.2 Repo 层

**`service/theme_state_repo.go`**：
- `GetByName` — 评估是否保留（向量合并可能需要精确名称预筛）
- `ListCandidates` — 删除
- `ExpireCandidates` — 改为 `ExpireDrafts`，只过期 draft
- 新增 `ListDraftsByUser(ctx, userID)` — 查用户所有 draft，升级判断需要
- 新增 `ListActivesByUser(ctx, userID)` — 查用户 active，早晚回顾需要
- 新增 `ListClosedByUser(ctx, userID)` — 查用户 closed，向量合并需要
- `ReactivateDraft(ctx, id, userID)` — 重新激活 closed → draft

### 3.3 Agent 层

**`agent/cron_runner.go`**：
- `dispatchPostChat` — 重新启用 `DiscoverDraftFromConversation` 调用
- `discoverOnce` — 改为早晚回顾：`MorningReview` / `EveningReview`
- `deliverOnce` / `DeliverPendingFindings` — 保留不变
- 新增配置字段：`NominateCountThreshold int`、`SpanDaysThreshold int`
- `SetPostChatDelay` 等 setter 方法保持

**`agent/reflection.go`**：
- `DiscoverDraftFromConversation` — 增强：
  - 在 nominate 前和已有 draft/active/closed 做向量相似比较
  - 每次 nominate 都创建新的 draft 记录（方案 B）
  - nominate 后检查升级条件：查该用户所有 draft，向量聚类后判断同主题次数和跨度
  - 满足条件 → 选最新 draft 升 active，关闭其余同主题 draft
- `deliverCandidate` — 删除
- `DiscoverAndDeliver` — 改为 `MorningReview` + `EveningReview`：
  - 遍历 active 主题，跑 ThemeAnalyst 分析
  - 产出 finding 写入 PendingFinding
- 新增 `CheckDraftUpgrade(ctx, userID, draftID)` — 判断是否该升级

### 3.4 Tool 层

**`tool/theme.go`**（`ThemeTool`）：
- `create_theme` 直接建 active（不再通过 candidate 审批）

### 3.5 前端 / 信箱

- `candidate_theme` 信箱项类型不再发送
- 信箱列表中 `candidate_theme` 的展示逻辑移除
- 主题管理页面的 candidate→active 审批逻辑精简
- 历史 `candidate_theme` 信箱项：标记为已处理

### 3.6 数据迁移

- 现有 `status='candidate'` 的主题 → 改为 `status='active'`（已经投递过，用户看到了就当采纳）
- 现有 `status='archived'` 的主题 → 改为 `status='closed'`
- 现有 `candidate_theme` 信箱项，状态改为 `handled` 或标记为已读

### 3.7 配置

**`conf/config.yaml`**（`reflection` 段）新增：

```yaml
reflection:
  draft_nominate_count_threshold: 3   # draft 升级需要的 nominate 次数
  draft_span_days_threshold: 3        # draft 升级需要的最小跨度（天）
```

### 3.8 间接影响

- `ReflectionOrchestrator` 构造函数：移除 candidate 相关参数
- `ThemeStore`：移除 candidate 相关方法
- `reflection_routes.go`：HookStatus 可能需调整
- `prompt-theme-discoverer.md`：prompt 中不再提 candidate，只说 draft 和 active
- `docs/adr/` 或 `docs/design/phase-6/` 中相关文档：标注为过时或更新

---

## 四、未覆盖的后续任务

以下暂不在本次改动范围内，另开设计文档讨论：

- **draft 过期机制**：draft 多久自动 close（目前 7 天），是否需要根据活跃度动态调整
- **active 衰减机制**：active 多久没有 new finding 自动 close
- **早晚 review prompt 细节**：09:00 和 20:00 的具体 prompt 设计
- **向量合并阈值独立配置**：当前 `cluster_threshold`（0.70）同时用于聚类和去重，是否拆分

---

## 五、边界情况

| 场景 | 预期行为 |
|------|----------|
| 同一会话多次 nominate 同一主题 | 每次建新 draft，允许——说明用户反复提及 |
| draft 刚升级 active 后，又发现相似 draft | 丢弃（已在 active 跟踪） |
| closed 主题被重新 nominate | 重新激活为 draft |
| 多个 draft 都满足升级条件 | 选最新一条升 active，其余同主题 draft close |
| 无历史 session 的新用户 | 无 draft，无 active，等待发现 |

---

## 六、可观测性

### 6.1 关键节点日志

| 节点 | 日志关键词 | 关键字段 |
|------|-----------|---------|
| draft 创建 | `[theme] draft created` | name, conv_id |
| 向量合并 | `[theme] draft merged` | new_name, existing_name, existing_status, similarity_score |
| 重新激活 | `[theme] draft reactivated` | name, old_closed_id |
| 升级检查 | `[theme] upgrade check` | name, nominate_count, span_days, satisfied |
| 升级执行 | `[theme] draft→active` | name, nominate_count, span_days |
| 回顾开始/结束 | `[theme] morning/evening review` | active_count, finding_count, duration_ms |

线上快速排查示例：

```bash
grep "draft created"     # 最近多少 draft 被发现
grep "draft→active"      # 多少成功升级
grep "draft merged"      # 向量合并命中情况
grep "review" | grep "finding_count"  # 早晚回顾产出
```

### 6.2 监控看板卡片

在 AdminView 概览页的 latency-grid 第 4 格（Embedding 耗时后、工具调用耗时前），新增主题追踪卡片。

**后端 snapshot**：`monitor.Register("themes", ...)` → 健康检查 API 返回：

```json
"themes": {
  "draft_count": 5,
  "active_count": 2,
  "closed_count": 12,
  "today_draft_created": 1,
  "today_upgraded": 0,
  "pending_findings": 3
}
```

**新增 repo 方法**：`ThemeStateRepo.Stats(ctx, userID)` — 统计各状态数量 + 今日新建/升级数。

**前端卡片**：NCard title="主题追踪"，展示三行 — 状态分布（draft/active/closed 数字）、今日活动、待投递 finding 数。不画图。
