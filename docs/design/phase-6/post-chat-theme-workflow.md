# 会话结束后主题处理方案

> 日期: 2026-05-16 | 状态: 设计阶段

---

## 一、动机

1. 当前绑定主题的会话结束后，不触发主题状态更新——聊天内容没有及时反映到主题
2. 未绑定主题的会话结束后，不尝试发现新主题——错失即时发现窗口
3. MemoryExtractor 是异步扫静默会话的，聊天→记忆→主题分析 链路太长，漏斗太深
4. 需要区分「状态维护」（静默）和「发现+推送」（定时），及时反馈但不过度打扰

## 二、核心设计：两个入口，一个核心

**一个核心**：给定一段文本 + 当前 state_text，产出新的 state_text。状态页维护规则统一（保留关键信息、标注时间、控制篇幅、淘汰过期），ThemeAnalyst 和 StateUpdater 共用。

**两个入口**：

| | StateUpdater（增量） | ThemeAnalyst（批量） |
|---|---|---|
| 何时触发 | 绑定会话结束后 | 反思循环（cron） |
| 数据源 | 新对话消息 | notes + memories |
| 输出 | 更新 state_text | findings + updated_state |
| 通知用户 | 否 | 是（走 Scorer+Secretary） |

## 三、30 分钟延迟，双路分发

```
聊天结束
  ↓
CronRunner.NotifyChatEnd(userID, conversationID)
  ↓ 30分钟后
  ├─ 有 context.theme_id？
  │    YES → StateUpdater：增量更新 state_text（静默）
  │    NO  → ThemeDiscoverer（消息维度）：发现草稿主题（静默）
  │
  ↓ 每日 cron
  RunOnce（全链路）：草稿 + 记忆维度发现 → 去重 → 分析 → 推送
```

## 四、草稿主题

### 4.1 存储

`theme_states(status=draft)`，携带 `metadata.candidate_info`（rationale/signal_strength/source_refs）。不创建 `mailbox_items`。

### 4.2 生命周期

```
新对话 → 草稿
  ↓
次日 cron 触发 → 和记忆维度发现一起去重 → 投递信箱
  ↓ 用户采纳
  draft → active
  ↓ 用户忽略
  draft → closed（过期清理）
```

### 4.3 优先级

- 定时 cron 时，草稿和新发现一起过去重，不跳过记忆维度的发现
- 草稿不等于跳过——它只是提前发现的候选，定时 cron 可能发现新的方向

## 五、增量更新的 cursor

### 5.1 存储

`conversation.extra.context.theme_state_cursor` —— 独立于 MemoryScheduler 的 `LastExtractedMsgID`。

生命周期：

| 操作 | 值 |
|------|-----|
| 创建 | 空（首条消息入会时填） |
| 读 | 取 cursor 之后的所有 `role=user` 消息 |
| 更新 | 状态更新完成后后移 |
| 会话被删 | conversation_id 查不到 → 主题 conversation_id=NULL，cursor 废弃 |

### 5.2 排序

用 `created_at` 排序（`id` 的 UUID 无序）。

## 六、去重标准

所有候选主题去重统一：

| 维度 | 做法 |
|------|------|
| 数据 | `name + " " + description` |
| 向量 | concept_embeddings(type=`theme_digest`) |
| 算法 | 余弦相似度 > `cluster_threshold`（0.70） |
| 范围 | `theme_states WHERE status IN ('active','candidate','draft')` |
| 方向 | 双向——草稿 vs 已有主题、新发现 vs 已有主题、草稿 vs 新发现 |

## 七、草稿投递（定时 cron 内）

草稿投递不靠 LLM 重新发现——它已有 name/description/metadata。代码做整合，LLM 做人话化+推送。

```
1. 代码前置：加载所有 status=draft 的草稿
2. 代码：ThemeDiscoverer（记忆维度）产出 candidates
3. 代码：drafts + candidates → 向量去重（name+desc embed, cosine > 0.70, 范围 active+candidate+draft）
4. 去重后存活的 drafts → LaunchAgent LLM：调 Secretary 人话化
5. LLM：调 mailbox.deliver(type=candidate_theme)
6. 代码后置：draft → candidate（投递过的草稿转为正式候选）
```

**和常规 candidate 流程的差异**：

| 步骤 | 常规 candidate | draft 投递 |
|------|---------------|-----------|
| 发现 | LLM 调 ThemeDiscoverer | 跳过，已存在 |
| 去重 | 代码 merge + dedupBySimilarity | 同上 |
| 人话化 | LLM 调 Secretary | LLM 调 Secretary |
| 投递 | LLM 调 mailbox | LLM 调 mailbox |
| 后置 | 创建 theme_states(status=candidate)+ mail | 更新 status draft→candidate |

**LLM 负责的部分**：Secretary（格式化人话）+ Scanner/打分 + Mailbox（投递）。代码负责的部分：加载草稿/发现结果、向量去重合并、状态流转。

## 八、定时的全链路

完全版 RunOnce（每日 cron）：发现→分析→打分→秘书→信箱。草稿附带直投。

非绑定会话的延迟 RunOnce：静默发现草稿，不投递。

## 八、过期清理

`RunOnce` 每次执行开头，扫一把过期候选和草稿：

```sql
UPDATE theme_states
SET status = 'closed'
WHERE status IN ('candidate', 'draft')
AND created_at < NOW() - INTERVAL 7 DAY
```

过期策略：
- `draft`：7 天未采纳 → closed。如果用户聊到了同一方向，下次对话会重新创建草稿
- `candidate`：7 天未操作 → closed。如果方向仍然相关，后续 ThemeDiscoverer 会重新提名
- `closed`：可被用户重新激活 → active

## 九、实施清单

| 文件 | 改动 |
|------|------|
| `model/theme_state.go` | status 枚举加 `draft` |
| `model/conversation.go` | `ConversationCtx` 加 `ThemeStateCursor` |
| `agent/cron_runner.go` | `NotifyChatEnd` 加 `conversationID` 参数，双路分发 |
| `agent/state_updater.go` | 新：增量 StateUpdater（对话→更新 state_text） |
| `agent/theme_discover.go` | `dedupBySimilarity` filter 范围扩至 `active+candidate+draft` |
| `agent/concept_embedder.go` | Type 加 `theme_digest`，创建主题时写入 |
| `agent/reflection.go` | `RunOnce` 支持静默模式（不投递信箱） |
