# 反思 Agent 调度拆分 & 分析下沉 & 对话后处理流程

## 现状

### 调度

当前反思系统所有产出都在 `runOnce` 中，由同一个 cron 频率（3 次/天）驱动：

```
runOnce:
  1. Launch Agent 调 ThemeAnalyst 分析所有活跃主题
  2. Scorer 排序发现
  3. Secretary 写文案 → mailbox 投递
  4. ThemeDiscoverer 发现候选主题 + draft 投递
```

问题：分析（需要新对话数据才有意义）和发现（纯数据驱动，积累够了就提名）被绑在同一频率。

### 对话后处理（当前有 2 条独立线）

| | 记忆提取 | 反思后处理 |
|---|---|---|
| **入口** | `MemoryScheduler` goroutine | `dispatchPostChat`（cronRunner） |
| **触发** | 每 10 分钟轮询，扫静默 >1h 的会话 | 对话结束回调 → 30 分钟后触发 |
| **做的事** | MemoryExtractor 提取记忆 | StateUpdater / DiscoverDraft |
| **耦合** | 完全独立 | 完全独立 |

问题：两个入口互不知情，记忆提取是 StateUpdater 检索的数据源之一，分开跑等于 StateUpdater 用的数据总是落后一步。且轮询全局静默会话太低效。

## 核心结论

用户的三类产出，本质属性不同：

| | 显式承诺 | 候选主题 | 主题简报 |
|---|---|---|---|
| **来源** | 单次对话中的明确承诺 | 跨时间数据聚类 | 已确认主题的增量分析 |
| **时效要求** | 高（精确到小时） | 低（天级别） | 中（有事才推） |
| **触发方式** | 对话后提取 + 定时推送 | 低频 cron | 对话后判定 |
| **生命周期** | 完成即结束 | 采纳→active / 忽略→closed | 持续追踪 |

## 方案设计

### 一、调度拆分

```
Cron A 投递窗口（高频，可配，如 09:00-22:00 每小时）:
  扫活跃主题 Metadata.pending_findings → 挑 TopN → Secretary → 投递 theme_finding

Cron B 发现（低频，1-2 次/天）:
  ThemeDiscoverer 发现候选主题 → Secretary → 投递 candidate_theme
  + draft 投递

对话事件（每次对话结束后 30 分钟，dispatchPostChat 为唯一入口，内部串行）:
  （见下方）
```

Launch Agent 不再负责主题分析（移除 prompt 中「对每个主题调 theme_analyst」步骤）。两个 cron 各自独立 timer，频率互不影响。

### 二、对话后处理入口合一

移除 `MemoryScheduler` 独立 goroutine，记忆提取合并进 `dispatchPostChat`，成为串行流程的第一步。

理由：
- 记忆是 StateUpdater 检索的数据源，先提记忆再更新状态，数据是新鲜的
- 从「轮询全局静默会话」变为「只针对刚结束的这一条会话」，不空转
- `memory_auto_extract` 开关保留，关闭时跳过第 1 步

### 三、对话后处理最终流程（dispatchPostChat 内部，唯一入口，串行）

```
1. 提取记忆（MemoryExtractor，针对当前会话）
   - memory_auto_extract=false 时跳过
   - 产出 user_memories + embedding chunks

2. 提取显式承诺（每次对话都做，不依赖主题绑定）
   - 1 次 ChatSync（非 ReAct），识别用户对话中的明确承诺
   - 能提取到截止时间 → 注册定时推送
   - 提取到承诺但无明确时间 → 暂存，后续结合状态扫描

3. 查会话是否绑定了主题
   │
   ├─ 绑定了 → StateUpdater 增量更新状态页 + 通用判定
   │    - 更新状态页
   │    - 顺带用通用判定条件评估是否有值得提醒的内容
   │    - 命中 → 产出 finding → 追加入 ThemeState.Metadata["pending_findings"]
   │    - 未命中 → 安静完成
   │
   └─ 没绑定 → DiscoverDraftFromConversation 发现草稿（不变）
```

### 四、通用判定框架

StateUpdater 在更新完状态页后，对照新旧状态，用通用语言判定（不定领域标签）：

1. **偏离基线** — 与状态页记录的正常状态比，有显著偏离？
2. **里程碑推进** — 状态发生了结构性变化？
3. **新外部信号** — 用户不直接产出数据、但系统感知到的新信息？
4. **模式形成** — 之前散落的数据点，现在能串成规律了？

任何一个命中 + 变化足够显著 → 产出 finding 追加入 `ThemeState.Metadata["pending_findings"]`（暂存，不投递信箱）。全不命中 → 不产。

推导建议（如「从数据看，建议 XX」）在此处自然融入——它是判定命中后的一种 finding 形态。

### 五、显式承诺提取

独立于主题体系。识别对话中用户明确表达的承诺或待办事项，提取截止时间。不需要 ReAct 循环，单次 LLM 调用完成。

#### 5.1 承诺模型

```go
type Commitment struct {
    ID             string     `gorm:"primaryKey;size:36"`
    UserID         string     `gorm:"size:36;not null;index"`
    Content        string     `gorm:"type:text;not null"`      // 承诺或待办内容
    Deadline       *time.Time                                   // 截止时间
    RecurrenceRule *string    `gorm:"type:json"`               // null=一次性，非空=循环规则
    NextDeadline   *time.Time                                   // 下一次触发时间
    Status         string     `gorm:"size:20;default:pending"` // pending / triggered / done / expired
    SourceConvID   string     `gorm:"size:36"`                 // 来源会话
    CreatedAt      time.Time  `gorm:"autoCreateTime"`
}
```

#### 5.2 循环规则

结构化 JSON，不定字符串：

```
// 每天 22:30
{"type": "daily", "time": "22:30"}

// 每周一、周三 09:00
{"type": "weekly", "time": "09:00", "days": ["monday", "wednesday"]}

// 每月 1 号、15 号 09:00
{"type": "monthly", "time": "09:00", "days": [1, 15]}

// 每个工作日 09:00
{"type": "weekday", "time": "09:00"}
```

四种类型覆盖日常中最常见的周期性表达。`nextDeadline(rule, from)` 纯计算函数。

| type | 算法 |
|------|------|
| `daily` | from 目标 time → 已过则 +1 天 |
| `weekday` | 往后找最近的非周末 → 设 time |
| `weekly` | 遍历未来 7 天，找第一个 days 匹配项 → 设 time |
| `monthly` | 遍历本月+下月，找第一个 days 匹配且未过的日期 → 设 time。日期当月不存在时取月末 |

#### 5.3 运转流程

```
提取到承诺 + 截止时间
  → 写入 Commitment（status=pending, next_deadline=xxx）
  → time.AfterFunc(next_deadline - 提前量) 注册一次性 timer

timer 触发
  → 创建 MailboxItem（type: "reminder"）
  → RecurrenceRule == nil → status=triggered，结束
  → RecurrenceRule != nil → 计算下一次 next_deadline，重注册 timer

进程重启 → 兜底扫描（每 5 分钟扫 pending 且 next_deadline < now + 5min）
```

一次性和循环同一套机制，区别仅在触发后是否「续约」。

#### 5.4 最小打扰间距

刚聊完的事用户自己记得，不宜立即推送。timer 触发时增加判断：

- 截止时间距现在 < 1 小时 → 跳过本轮，不推送
- 循环承诺首次触发在 1 小时内 → 跳过第一轮，从下一轮开始

提取照做、timer 照注册，仅在推送前做过滤。

无截止时间的承诺提取出也写表（status=pending, deadline=null），留待后续结合状态扫描——MVP 不做。

### 六、StateUpdater 升级

现 StateUpdater 只做「增量更新状态页文本」。升级后：

- 更新完状态页后，追加通用判定逻辑
- 命中时产出 finding（含 title/content/source_refs）
- 暂存至 `ThemeState.Metadata["pending_findings"]`，不直接投递
- 等待窗口期 cron 统一清扫投递

### 七、配置

```yaml
reflection:
  deliver_window: "hourly"     # 投递窗口频率
  deliver_window_hours: [9,22] # 窗口时间范围（可选，如只在白天推送）
  discover_schedules: ["09:00", "18:00"]
```

### 八、待移除

- `MemoryScheduler` goroutine（`internal/agent/scheduler.go`）
- main.go 中 `go memScheduler.Start(...)` 启动代码
- Launch Agent prompt 中「对每个主题调 theme_analyst」相关步骤
