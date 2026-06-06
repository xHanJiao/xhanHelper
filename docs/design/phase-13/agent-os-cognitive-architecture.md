# Phase 13: Agent OS 认知架构 — Theme / Mission / Signal

## 愿景

xhanHelper 的定位从「对话工具」升级为**个人参谋/秘书**：用户将所有事情告诉它，和它深入讨论特定事项。Agent 在对话中提供跨来源的深度关联认知，在合适的时机主动开口干预。

这不是一个功能迭代，而是一次认知架构的重新定义——系统如何**理解**用户世界，如何**记住**承诺，如何**判断**干预时机。

---

## 三层架构

```
┌─────────────────────────────────────────────────┐
│                  Signal（判断层）                 │
│        系统自主判断何时该主动开口                   │
│        「我觉得你该看看这个」                       │
├─────────────────────────────────────────────────┤
│                 Mission（承诺层）                  │
│        用户托付的追踪任务，对话中自然管理            │
│        「我替你盯着这件事」                         │
├─────────────────────────────────────────────────┤
│                  Theme（认知层）                   │
│        对用户世界的结构化理解                       │
│        「我了解你」                                 │
├─────────────────────────────────────────────────┤
│           Memory / Note / Message（数据层）        │
│        原始事实、记录、对话                          │
└─────────────────────────────────────────────────┘
```

---

## Theme — 认知层

### 定义

Theme 是系统对用户世界的**结构化认知**。它把碎片信息（消息、笔记、记忆、账单）升维成 Agent 对用户生活领域的**理解**。

### 核心问题

> 这个用户的世界是什么样的？

### 职责

- 从对话中自动发现用户的生活领域和关注点
- 维护每个领域的「状态页」——不是事实列表，而是 Agent 对该领域的理解
- 跨来源关联（消息、笔记、记忆、账单）
- 在对话中提供认知深度：当用户聊到某个话题时，Agent 能拿出对这件事的完整理解

### 不做什么

- 不主动通知用户（那是 Signal 的事）
- 不执行用户的追踪承诺（那是 Mission 的事）
- 不要求持续性——一个主题可以只是「用户最近在想的事」，哪怕只出现一次

### 生命周期

```
discovered → active → (无新活动后衰减关闭)
```

- **创建**：ThemeDiscoverer 从对话中自动发现，不再要求持续性门槛（去掉 3 次提名 / 3 天跨度的升级逻辑）
- **更新**：StateUpdater 在对话后增量更新状态页
- **深度分析**：ThemeAnalyst 定时对近期有新活动的主题做深度分析
- **关闭**：无新活动后按 duration 自动衰减关闭（ephemeral=7d, short=14d, medium=30d, long=90d）

### 上下文注入

对话时，ThemeSource 按向量相似度检索与当前话题相关的主题，将其状态页注入 System Prompt，让 LLM 在对话中自然引用已有认知。

### 关键设计变化（Phase 13）

**创建**：
1. **去掉 draft 状态**：ThemeDiscoverer 发现即 active，不再需要多轮提名升级。`dedupBySimilarity` 简化为 3 分支——撞 active 则 reject，撞 closed 则 reactivate 为 active（保留旧 state_text），无匹配则新建 active
2. **创建时写初始状态页**：ThemeDiscoverer 提名时 LLM 已产出 rationale，直接写为 state_text，不留白等 Analyst
3. **放开创建条件**：prompt 从「需要长期追踪价值」改为「用户生活中有意义的方面」

**更新**：
4. **StateUpdater 改为记忆驱动**：新增 `UpdateFromMemory(themeID, memory)`，输入为 MemoryExtractor 产出的记忆（而非原始消息）。dispatchPostChat 中用记忆做多路召回（topic + tags + embedding）匹配活跃主题，命中则独立更新
5. **删除旧的 `UpdateFromConversation`**：连带 `Conversation.ThemeStateCursor` 字段和 cursor 逻辑
6. **MemoryExtractor 嵌入同步化**：`storeMemory` 中异步 goroutine 改为同步，确保向量在 StateUpdater 前就绪

**分析**：
7. **ThemeAnalyst 按需分析**：`reviewActiveThemes` 加 `UpdatedAt > LastRunAt` 判断，没新活动就 skip
8. **ThemeAnalyst 不再产出 finding**：prompt 改为只输出 updated_state 和 summary，不产 deliverable finding。通知是 Mission/Signal 的事

**关闭**：
9. **Active 自动衰减保留**：`expireActiveThemes` 按 duration 关闭（long=90d, medium=30d, short=14d, ephemeral=7d）
10. **删除 draft 过期逻辑**：`themeStore.Expire()` 和 MorningReview/EveningReview 中的 expire 调用删除

**其它**：
11. **配置清理**：删除 `draft_nominate_count_threshold` / `draft_span_days_threshold` / `draft_expire_days`
12. **DB 迁移**：现有 draft → active
13. **重放独立主题阈值**：全链路加 `ThemeThreshold` / `ThemeLimit`，和记忆/笔记/历史平权
14. **前端清理**：ThemeView 删除 candidate/采纳/忽略 UI

---

## Mission — 承诺层

### 定义

Mission 是用户通过对话**托付给系统的追踪承诺**。用户说「帮我盯着」，系统就定期检查并汇报。

### 核心问题

> 用户交代了什么事要我盯着？

### 职责

- 接受用户的追踪委托（对话中 LLM 调 `mission.create_mission`）
- 定期检查，对比当前状态与上次状态
- 发现变化或停滞时，投递信箱通知用户
- 生命周期在对话中自然管理——用户说「不用盯了」时 LLM 调 `pause_mission`

### 不做什么

- 不自动从主题中发现（那是 Signal 的事—未来可做，Phase 13 不包含）
- 不让用户手动在页面上管理（生命周期在对话中流转）
- 不过度分析——分析是 Theme 的职责，Mission 聚焦于对比和判断

### 生命周期

```
active ↔ paused → completed
```

- **创建**：用户对话中说「帮我盯着 X」，LLM 调 `mission.create_mission`
- **检查**：MissionRunner 按 cron 定期执行 ReAct 循环，对比状态变化
- **通知**：有值得关注的变化时，创建 MailboxItem(type=mission_check)
- **管理**：LLM 在对话中调 `mission.pause_mission` / `mission.resume_mission` / `mission.complete_mission`
- **前端**：只查看和手动触发检查，不做状态管理

### 设计要点

- Theme 和 Mission 是**独立实体**，通过 ThemeID 做可选关联
- Theme 对 Mission 是 **enhancer，不是 dependency**——有 Theme 时判断更精准，没 Theme 也能独立跑
- MissionRunner 保留 ReAct 循环，但目标从「深度分析」变为「轻量检查」：查当前状态 → 对比判断 → 决定是否开口
- Mission 不重复 Theme 的分析工作。分析归 Theme，Mission 只做对比和决策
- 对话中通过 MissionSource 向量检索相关 Mission，注入 System Prompt

### 运行示例

**没有 Theme 的 Mission**：
```
用户说 "每天下午三点提醒我喝水"
→ LLM 调 mission.create_mission(check_cron="daily")
→ MissionChecker 触发：时间接近 15:00？上次不是今天？→ 投递 "该喝水了"
→ 纯时间判断，不需要检索数据
```

**有 Theme 的 Mission**：
```
Theme "睡眠追踪" 状态页已有睡眠习惯分析
用户说 "超过十一点半还没睡就戳我"
→ LLM 调 mission.create_mission(check_cron="daily", theme_id=...)
→ MissionChecker 23:30 触发：查看 Theme 状态页了解习惯 → 查最近消息时间戳 → 判断
→ Theme 提供认知深度，让判断更精准
```

---

## Signal — 判断层

### 定义

Signal 是系统的**自主干预决策**。不需要用户托付，不需要用户同意——系统基于对用户世界的理解，自己判断何时该开口、该说什么。

### 核心问题

> 我应该什么时候主动开口？

### 职责

- 基于 Theme 的认知 + 时间维度，发现值得干预的信号
- 「沉默本身是信号」——某件事 45 天没有新动态，这本身就是值得开口的原因
- 跨主题关联——把不同领域的信息连在一起，形成用户可能没意识到的关联
- 在合适的时机投递信箱通知

### 和 Mission 的本质区别

| | Mission | Signal |
|---|---|---|
| 谁发起 | 用户托付 | 系统自主 |
| 做什么 | 执行承诺 | 判断干预 |
| 内容 | 「你让我盯的 X，进展是...」 | 「我注意到 Y，你该看看」 |
| 周期 | 持续追踪直到用户叫停 | 一次性开口，或在条件持续满足时再次开口 |

### 例子

- Mission：「你让我盯的求职进度——上次投递是 45 天前，依然没有新反馈」
- Signal：「你这周餐饮支出 ¥800，同时你之前说过要健身——知行之间有个裂缝」

- Mission：「DeepSeek 月度支出 — 本月 ¥100，低于上月」
- Signal：「你连续三个月在月底焦虑现金流，要不要设定每月 25 号的预算提醒？」

### 当前状态

Phase 13 只做概念定义。实现留到后续 Phase。

---

## 对话中的协作

当用户和 Agent 讨论时，System Prompt 同时注入三层信息：

```
## 你的理解
[ThemeSource] 用户最近在关注职业转型，状态是...

## 你的承诺
[MissionSource] 你帮用户盯了求职进度，最近一次检查发现...

## 你的判断
[Signal] 待 Phase 14+ 实现
```

Theme 让对话有深度，Mission 让 Agent 不忘记承诺，Signal 让 Agent 在该开口时开口。

---

## 数据架构总览

```
数据层:
  Message ─→ Memory ─→ Note ─→ Bill
       │                            │
       ▼                            ▼
认知层:  Theme (状态页) ──可选引用──→ Mission (追踪任务)
       │                                    │
       │    ┌───────────────────────────────┘
       ▼    ▼
判断层:  Signal (干预决策) ← 未来
              │
投递层:       ▼
            Mailbox
```

Theme 和 Mission 是独立实体。Theme 通过 ThemeID 被 Mission 可选引用——有 Theme 提供认知深度，没 Theme 也能独立运行。

---

## Phase 13 实施范围

1. **Theme 改造**：去掉 draft → active 升级门槛，StateUpdater 记忆驱动，ThemeAnalyst 按需分析且不产 finding
2. **Mission 重新定位**：仅对话创建/对话管理，MissionRunner 聚焦轻量对比判断
3. **Signal 概念定义**：暂不实现，文档先行
4. **重放独立主题阈值**：支持实验和调参
5. **代码清理**：删除 draft 相关逻辑、cursor 逻辑、candidate UI

### 分批实施

| 批次 | 范围 |
|------|------|
| 第一批 | dedupBySimilarity 简化 + ThemeDiscoverer prompt + 初始状态页 |
| 第二批 | StateUpdater 记忆驱动 + MemoryExtractor 同步嵌入 |
| 第三批 | ThemeAnalyst 按需分析 + 不产 finding |
| 第四批 | 删除 dead code + 配置清理 + 前端清理 |
| 第五批 | 重放主题阈值 + DB 迁移 |

后续 Phase：
- Phase 14: Signal 引擎实现
- Phase 15: Theme 上下文检索增强（跨主题关联）
- Phase 16: 对话中 LLM 管理 Mission 生命周期的完整验证
