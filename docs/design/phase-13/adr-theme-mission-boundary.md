# ADR: Theme / Mission 边界重定义

## 背景

系统当前有三个概念层级——Theme（主题）和 Mission（任务）。然而两者在功能上存在重叠：都在做「分析 + 通知」，边界模糊。Mission 的名字暗示用户任务，但实际使用中它被赋予了系统主动发现追踪目标的能力，这与其定义冲突。

## 决策

重新定义三层架构：

1. **Theme（认知层）**：系统对用户世界的结构化理解。不主动通知，只在对话中提供认知深度。
2. **Mission（承诺层）**：用户通过在对话中托付的追踪任务。独立于 Theme 存在，通过 ThemeID 做可选关联。
3. **Signal（判断层）**：系统自主的干预决策。不需要用户托付或同意，基于对用户世界的理解，在合适时机主动开口。

## 关键变化

### Theme
- **去掉持续性门槛**：draft → active 不再需要 3 次提名 + 3 天跨度。ThemeDiscoverer 发现即激活。
- **放开创建**：不要求长期追踪价值，任何用户关注的话题都可以成为主题。
- **按需分析**：ThemeAnalyst 只分析近期有新活动的主题。
- **自负清理**：短周期 auto-close（ephemeral=7d, short=14d）。

### Mission
- **来源单一化**：Phase 13 仅支持对话创建（LLM 调 `mission.create_mission`）。
- **独立实体**：Theme 和 Mission 互不依赖。Theme 通过 ThemeID 被 Mission 可选引用——Theme 是 enhancer，不是 dependency。
- **轻量检查**：MissionRunner 保留 ReAct，但只做对比和时机判断，不做深度分析。深度分析是 Theme 的职责。
- **生命周期对话管理**：暂停/恢复/完成在对话中通过 tool call 完成，前端只查看。
- **移除 suggested 状态**：创建的 Mission 直接 active。

### Signal
- Phase 13 仅做概念定义，不做实现。

## 理由

- Theme 的持续性门槛导致大量 draft 被卡在升级流程中（当前 25 个主题中 3 个是 draft），同时活跃主题只有 4 个
- 放开 Theme 创建后活跃主题预计涨到 10-15 个，成本控制在 2-3 倍（单用户场景无压力）
- Mission 定位为「用户交代的任务」使来源清晰，生命周期管理更自然
- Signal 作为独立概念避免了在 Theme 和 Mission 之间塞入「不纯粹」的功能
- Theme 和 Mission 独立运行，避免循环依赖：Mission 不需要 Theme 也能工作，有 Theme 时更精准

## 数据验证

当前数据库（2026-06-06，单用户）：
- 25 个主题：4 active, 3 draft, 18 closed
- 72% 已 close —— 自动衰减机制有效
- 3 个 draft 被门槛卡住
- 日均新增 1-2 个主题
- 日均 40-50 条用户消息

去掉门槛后预估稳态 10-15 active，ThemeAnalyst 调用量从 ~10 次/天增至 ~20-30 次/天（Pro 模型），成本可控。
