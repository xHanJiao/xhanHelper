# 用户画像 Fact 化重构 — 设计文档

> 版本：v3 | 日期：2026-08-16 | 状态：设计完成，待开发

## 1. 背景与问题

画像（`user_profile`）现状：10 字段白名单 + **一字段一整段文本**，无长度上限、无内容管理、无事实级溯源。

### 1.1 实际数据表现（2026-08 线上数据）

**职业领域** 一段混装 5 条独立事实，其中多条带时效性：

```
5年Go后端（字节/百度/图森），AI辅助转型全栈（Go+Vue+iOS），自研AI Agent产品（xhanHelper），
gap期求职中，正面试具身智能方向（光轮智能数据闭环岗位）；过去两份工作不足一年，
2026年4月短暂入职，5月离职
```

- `5年Go后端` / `AI辅助转型全栈` / `自研AI Agent产品` —— 稳定事实
- `gap期求职中` —— 找到工作即过时
- `过去两份工作不足一年，4月短暂入职5月离职` —— 新工作稳定后过时

**补充说明** 338 字散文杂烩（"学狗叫"、"睡眠差"、"房价观察"混装）——本质是混杂 facts，装在一个字段里。

**核心痛点**：
1. 过时信息永久粘附——单值覆盖模式下，LLM 只有"整字段重写"才可能淘汰旧信息，实际长期不重写；时效性条目（gap期求职中）无法单独淘汰
2. 画像随对话无限膨胀 → 资源区 profile 预算（1600）持续被挤压
3. 没有事实级溯源：不知道某条信息何时产生，冲突判断、过时识别无依据

**LLM 供应商涨价背景**：方案需同步压缩提取与注入的 token 消耗。

## 2. 目标

1. **field → fact 三级结构**：字段拆分为独立事实，实现事实级溯源、精确管理、时效性条目自动淘汰
2. **内容管理**：过时事实自动淘汰（LLM 重述时省略 = 淘汰），画像保持新鲜
3. **token 节约**：字段定义极简分流、outdated 不进 LLM 上下文、caveman 高密度规则、无变化跳过写入
4. **单一写入通道**：砍掉 profile_tool，画像只由后处理 worker 维护

## 3. 数据模型

### 3.1 新表 `user_profile_facts`

```sql
CREATE TABLE user_profile_facts (
  id         VARCHAR(36) NOT NULL PRIMARY KEY,   -- uuid
  user_id    VARCHAR(36) NOT NULL,
  field      VARCHAR(64) NOT NULL,               -- 白名单字段名
  value      TEXT NOT NULL,                      -- 单条事实
  status     VARCHAR(16) NOT NULL,               -- active / outdated
  batch_id   VARCHAR(36) NOT NULL,               -- 同次提取批次（版本管理 + 清理）
  created_at DATETIME NOT NULL,
  INDEX idx_facts_user_field (user_id, field, status),
  INDEX idx_facts_batch (batch_id)
);
```

```go
type UserProfileFact struct {
    ID        string    `gorm:"primaryKey;size:36"`
    UserID    string    `gorm:"size:36;not null"`
    Field     string    `gorm:"size:64;not null"`
    Value     string    `gorm:"type:text;not null"`
    Status    string    `gorm:"size:16;not null;default:active"`
    BatchID   string    `gorm:"size:36;not null"`
    CreatedAt time.Time `gorm:"autoCreateTime"`
}
```

**无 source 列**：原 source 语义（explicit = 用户明确陈述高置信 / llm_additional = LLM 自行判断低置信）在唯一写入通道下已冗余——llm_additional 只可能是"补充说明"字段（其余 9 字段提取规则都是"只提取用户明确陈述"），行级 source 完全由 field 决定。置信度差异由 `ProfileFieldDef` 派生（补充说明字段带低置信标记，注入 XML 如需提示从字段定义生成），不依赖行数据。

- **Field 层** = 白名单 + 聚合视图（active facts 按时间拼接），不再存整段大文本
- **fact 层** = 单条短事实，带 created_at 溯源、status 生命周期
- 旧 `user_profile` 表**保留不删**（延迟迁移读取 + 回滚兜底），代码停用写入

### 3.2 字段分类与配额（终版）

| 字段 | Kind | MaxLen（字） | MaxFacts | 说明 |
|------|------|-------------|----------|------|
| 称呼 | single | 10 | 1 | 单值覆盖 |
| 出生日期 | single | 10 | 1 | 原"出生年份"；值格式 `YYYY-MM-DD`，仅知年份时 `YYYY` |
| 籍贯 | single | 30 | 1 | 单值覆盖 |
| 性别 | single | 2 | 1 | 单值覆盖 |
| 职业领域 | list | 60 | 6 | 履历条目列表（实际 20-40 字），天然多 fact（"gap期求职中"等时效性条目） |
| 重要关系 | list | 80 | 10 | 每人一条 fact（线上最长 ~50 字，80 留余量容纳多人称/细节简介） |
| 饮食习惯 | list | 60 | 5 | 偏好条目列表 |
| 财务概况 | list | 50 | 5 | 事实条目（存款/收入/计划，实际 10-20 字） |
| 长期目标 | list | 50 | 5 | 目标条目列表（实际 10-30 字） |
| 补充说明 | list | 30 | 10 | 现状就是混杂 facts（"学狗叫"≈7 字、"睡眠差"≈12 字），拆成短事实条目 |

- **list**：多条 active fact，聚合注入（`fact1; fact2`）
- **single**：1 条 active fact，语义 = 覆盖写入
- **单值字段也进 facts 表**（1 条 active = 覆盖语义），统一模型零特判
- **配额限制在单条 fact 级别**：MaxLen 是每条 fact 的长度上限，MaxFacts 是条数上限。字段总长 = MaxLen × MaxFacts 自然推导（如补充说明 30×10 = 300 字），**不设字段级总长限制**——避免只限总长导致长度分布不均（1 条 300 字 + 9 条空 vs 10 条各 30 字）

**出生日期变更细节**（生日是重要信息）：
- ExtractDesc 明确：用户提到月份日期时必须记录完整日期（"我 3 月 15 生日"→"1994-03-15"）；仅说"94年的"时记年份 "1994"
- 延迟迁移时旧字段"出生年份"映射到新字段"出生日期"

### 3.3 迁移策略：延迟迁移（零 SQL 数据迁移）

**问题**：旧行混杂多 facts（重要关系一行里 CJ/又又/惠茜三个人物），整体转单条巨型 fact 会让后续提取被迫整体重写，fact 粒度失效。字符串规则拆分不可靠（同分隔符启发式拆散文的理由，已否决）。

**方案**：不做迁移脚本，让**第一次增量提取自然完成拆分**：

```
facts 表为空 + 旧表有数据
  → 提取时把旧表整段值作为"初始 existing"展示给 LLM
    （标注"这是历史画像文本，请拆分为独立事实列表"）
  → LLM 输出该字段事实集 → 写入 facts 表（active）
  → 旧表此后不再读取
```

- 拆分质量 = LLM 语义级，零字符串启发式
- 零额外 LLM 调用（复用当次 session_end 提取）
- **字段名映射**：旧"出生年份" → 新"出生日期"（展示在出生日期字段下）
- **注入路径不回退**：facts 表空则注入空 profile（不读旧表）
- 风险：用户长期不说话则旧值不迁移——但无对话即无提取，行为不变；首次对话后自动完成

## 4. 写入路径（ProfileWorker）

### 4.1 输出格式

统一"字段 → 字符串数组"（单值字段 = 1 元素数组，worker 统一处理）：

```json
{"职业领域": ["5年Go后端", "已入职新公司"], "出生日期": ["1994-03-15"], "补充说明": ["睡眠差"]}
```

JSON 键用中文（LLM 稳定、无映射错误），不缩写成英文键（可读性换 token 不值得）。

**输出范围（非全量）**：
- **增量提取**：仅输出**本次有变化的字段**，每个输出字段给**该字段的完整事实集**（含保留的旧事实原文）——worker 是全量替换逻辑，LLM 的"省略某条旧事实"就是"判定过时"的信号，所以变化字段必须给完整列表而非仅新增部分
- **未输出的字段不动**（worker 只处理 JSON 中出现的字段）
- 完全无变化 → 输出 `{}`
- **冷启动例外**：首次提取输出所有找到的字段（全量）

### 4.2 替换逻辑（零文本 diff，全部确定性操作）

```
1. 归一化（去空白/标点）排序后精确比较：
   LLM 输出数组 == active facts 数组 → 跳过（无变化零写入）
2. 不一致 → 旧 active 批量标 outdated（关联 BatchID）→ 新 facts 插入 active
3. 配额校验（rune 计数）：
   - 校验失败 → 反馈 LLM 重新生成（最多重试 2 次）
     "以下输出不满足约束：字段'重要关系'第 2 条 86 字，超过上限 80 字；
      字段'补充说明'共 12 条，超过上限 10 条。请精简/拆分后重新输出完整 JSON"
     —— 正常路径零额外成本（字段清单带 ≤N字 约束，超限罕见）；
     LLM 自行精简/拆分，fact 语义完整
   - 重试仍超限（极罕见）→ 才截断兜底 + log（保留信息主体）
4. 清理：outdated 保留最近 3 个 batch，更旧物理删除
```

**为什么放弃字符串 diff**（讨论中逐场景推演否决）：

| 失败场景 | 示例 | 结果 |
|---------|------|------|
| A 词序交换 | old `不吃辣，喜欢面食` → new `喜欢面食，不吃辣` | 归一化不相等、无包含 → 误 outdated + 重插 → 同一信息两条 |
| B 中间插词 | old `有房贷, 收入稳定` → new `有房贷，收入目前稳定` | "目前"插中间 → 子串断裂 → 误判 |
| C 等价改写 | old `微醺偏好：330ml啤酒即可` → new `喜欢330ml啤酒微醺` | 无任何字符串关系 → 误判 |
| D 拆分/合并 | 两条合一、一条拆两 | 集合 diff 无稳定解 |

字符串启发式永远在猜，每补一条规则（词序、同义词、缩写）复杂度递增、收益递减——无底洞。**diff 交给 LLM 的语义判断**：它省略某条 = 认为过时；worker 只做"删了重插"（插入、标记、聚合、去重判断，零文本匹配）。

**漏写风险（接受）**：LLM 重述时漏掉某条 → 误标 outdated → 永久丢（除非用户再次提及）。缓解：active 全量 + "原样保留"强规则，LLM 漏写概率低；outdated 留在 DB 可人工审计；对话再次提及会重新提取。

### 4.3 Outdated 机制（审计保留 + 防膨胀双闸）

**膨胀推演（正常场景，LLM 守"只输出变化字段"规则）**：

| 轮次 | 对话 | LLM 输出 | active | outdated |
|------|------|---------|--------|----------|
| 1 | "我在面试" | ["5年Go后端","gap期求职中"] | 2 条 | 0 条 |
| 2 | "今晚吃什么" | （无变化 {}） | 2 条 | 0 条 ✓ |
| 3 | "拿到 offer 了" | ["5年Go后端","已入职"]（省略 gap期） | 2 条 | 2 条 |
| 4 | "4月待过一家公司" | 三条（新增一条） | 3 条 | 4 条 |

膨胀速度 = 每轮"有变化的字段"的旧 active 数。正常轮次 1-3 字段变化 → 每轮 +5~15 条 outdated。

**失控场景（LLM 每轮全字段重写）**：每轮 outdated += 当前 active 总数，而 active 持续累积 → 第 n 轮 outdated 总量 ≈ 1+2+...+n = **O(n²) 增长**。

**两道闸**：
1. **无变化精确比较跳过**（核心防线）：守规则时只有变化字段产生 outdated
2. **DB 保留最近 3 个 batch 物理清理**（存量硬上限）：即使 LLM 不守规则，outdated 有限

**outdated 进不进 LLM 上下文？** 三个选项比较：

| 选项 | LLM 上下文 | DB | token | 漏写恢复 |
|------|-----------|-----|-------|---------|
| 1. 无 outdated | 只 active | 替换即物理删 | 最省 | 永久丢 |
| 2. 审计保留（**选定**） | 只 active | outdated 保留（人工可查） | 最省 | 不可自动恢复 |
| 3. 恢复窗口 | active + 最近 1 批 outdated | 保留 3 批后清理 | +1~1.8k chars/次 | 可恢复上一轮漏写 |

**选定选项 2**：漏写恢复窗口其实很窄（"上一轮漏的"且"用户再也不提"才损失），而 LLM 看到 active 全量 + "未过时必须原样保留"强规则后，真漏写概率低。涨价背景省 token 优先——**active 全量给 LLM，outdated 只审计不给 LLM 看**（也避免 LLM 把过时信息写回）。膨胀只发生在 DB（有清理闸），LLM 上下文与 token 完全不受影响。

### 4.4 Prompt 设计（token 优先，逐项讨论结论）

**a. 字段定义分流**：

| 场景 | 内容 | 估算 |
|------|------|------|
| 冷启动（每用户一次） | 完整 ExtractDesc（含提取边界引导，ReAct 搜索式需要完整理解"什么值得提取"） | ~2k chars，可忽略 |
| 增量提取（常态） | 极简清单：`职业领域(≤120字): 当前行业岗位方向`、`出生日期(≤10字,YYYY-MM-DD): 用户生日` | ~200 chars（砍 90%） |

- 提取边界规则（"只提取明确陈述的，不推断"）是字段共有 → 放全局 rules，不重复 10 次
- 实现：`ProfileFieldDef` 加 `BriefDesc`（一句话），冷启动/增量分别生成

**b. Existing 上下文**：只给 **active facts 全量**（带 created_at，判断时效）；outdated 不进上下文（见 4.3 选项 2）。

**c. Caveman 规则**（4 条，规则文本本身也要省）：

```
- 事实短句：无引导词（不写"用户表示/他认为"），直接写事实
- 并列项用分号分隔；禁散文长句、禁重复
- 已有事实未过时必须原样保留（复制原文）；已过时的省略
- 只输出有变化或需修订的字段；无变化输出 {}
```

预期效果：职业领域 150 字散文 → 5-6 条 20-40 字短 fact。

**d. 冷启动轮次 9 → 6**：冷启动要覆盖 10 字段，1 轮宽泛搜索 + 5 轮精确 ≈ 6 轮够；砍掉的 3 轮最多导致个别字段留空 → 增量提取补齐。

**e. 短 session 不做跳过**："我老家郑州"仅 6 字也是高价值画像。跳过省一次 Flash 调用（~$0.001），不抵丢画像风险。

### 4.5 写入流程

```
Process(sess):
  hasAnyFacts = factRepo.HasActive(userID)
  if !hasAnyFacts && 旧表有数据 → 延迟迁移模式（existing = 旧表整段值）
  if !hasAnyFacts && 旧表无数据 → 冷启动 ReAct（6 轮，unified_search）
  else                          → extractNormal（单次 ChatSync，无工具）
```

- 只扫 user 消息（AI 消息不提取）
- 用户明说的新事实与旧值冲突 → 以新为准（全量替换天然覆盖）
- 白名单过滤：不在 ProfileFields 中的字段直接丢弃
- source 概念取消：置信度差异由字段定义派生（补充说明 = LLM 自行判断，其余 = 用户明确陈述）

## 5. 注入路径（聊天资源区）

- `buildProfilePackets`：读 active facts → 按字段聚合（`fact1; fact2`）→ 现有 `ProfilePacket` 输出格式不变
- **聊天注入不带 created_at**（不必向主 LLM 全透露）；只有提取上下文带 created_at
- 聚合长度 = 实际 fact 数 × 实际长度（常态 ≈ 1-1.5k 字）< 资源区 profile 预算 1600 ✓
- 理论上限 ≈ 2.3k 字（MaxFacts × MaxLen 全满，实际不可能达到）——资源区 per-type 预算兜底（profile 最后砍）

## 6. Profile Tool 砍除

| 项 | 处理 |
|----|------|
| `tool/profile_tool.go` + `_test.go` | 删除 |
| 注册（appinit/tools.go） | 移除 |
| seeder 种子（profile） | 删除 |
| retiredBuiltinTools | 加 `profile`（线上 DB 残留置 inactive） |

画像写入 = worker 自动提取**单一通道**。影响：主对话 LLM 不能再主动改画像（线上 30 天仅 6 次调用，低频，损失可接受）。worker 冷启动 ReAct 用 unified_search，不受影响。

## 7. 文件清单

| 类型 | 文件 | 改动 |
|------|------|------|
| 新建 | `internal/model/user_profile_fact.go` | UserProfileFact 模型 |
| 新建 | `migrations/021_user_profile_facts.sql` | 建表 + 索引 |
| 新建 | `internal/service/user_profile_fact_repo.go` | 批量替换（旧标 outdated→新插入）、按 user/field 聚合查询、HasActive、按 batch 清理（留 3 批） |
| 改动 | `internal/profilefield/profilefield.go` | ProfileFieldDef 加 Kind/MaxLen/MaxFacts/BriefDesc；出生年份→出生日期；极简清单生成函数 |
| 改动 | `internal/agent/profile_worker.go` | existing 读 facts（空时读旧表延迟迁移）；提取 XML 改 fact 格式 + 极简字段 + caveman 规则；输出改数组；替换逻辑；MaxLen/MaxFacts 截断；冷启动 6 轮；batch 清理调用 |
| 改动 | `internal/agent/orchestrator.go` | buildProfilePackets 改读 facts active 聚合（注入格式不变） |
| 删除 | `internal/tool/profile_tool.go` / `_test.go` | 砍除 |
| 改动 | `internal/appinit/tools.go` / `seeder.go` | 注册/种子清理 + retired 名单 |
| 改动 | `internal/appinit/init_db.go` / `init_repos.go` | AutoMigrate 新表 + FactRepo 注入（旧 UserProfile 表保留供延迟迁移读） |
| 新建 | 测试 | repo 批量替换/聚合/清理；worker 替换/跳过/截断/延迟迁移路径；profilefield 配额完整性 |

## 8. 验证

- `go build ./...` + `go test ./...` 全绿
- 迁移 SQL 用真实旧数据样例手动验证一次
- 真机观察：首轮对话后画像拆分质量（延迟迁移）、outdated 清理、资源区 profile 块大小回归（< 1600）

## 9. 决策记录

| 日期 | 决策 |
|------|------|
| 2026-08-16 | 放弃字符串 diff（重述漂移/词序/合并拆分无解，场景 A-D 推演否决），改为 LLM 语义判断 + worker 全量替换（零文本匹配） |
| 2026-08-16 | outdated 不进 LLM 上下文（三选项比较选"审计保留"），DB 保留最近 3 批；防 O(n²) 膨胀双闸（无变化跳过 + batch 清理） |
| 2026-08-16 | 延迟迁移：facts 表空 + 旧表有数据 → 首轮提取 LLM 拆分旧值；注入不回退读旧表 |
| 2026-08-16 | 补充说明 list 化：30 字/条，最多 10 条（现状就是混杂 facts） |
| 2026-08-16 | 重要关系 MaxLen 80→120（容纳长人物条目） |
| 2026-08-16 | 出生年份 → 出生日期（生日是重要信息），值格式 YYYY-MM-DD（仅知年份时 YYYY），旧数据字段名映射 |
| 2026-08-16 | 短 session 不做跳过（丢画像风险 > 节省） |
| 2026-08-16 | profile_tool 砍除（单一写入通道 = worker） |
| 2026-08-16 | 冷启动轮次 9→6；增量提取字段定义极简分流（砍 90%） |
| 2026-08-16 | 输出格式统一"字段 → 字符串数组"（单值 = 1 元素），JSON 键用中文 |
| 2026-08-16 | 删 source 列（唯一写入通道下 llm_additional 只可能是补充说明字段，行级 source 由 field 决定，冗余） |
| 2026-08-16 | 配额限制在单条 fact 级别（MaxLen 单条截断 + MaxFacts 条数截断），字段总长自然推导，不设总长限制 |
| 2026-08-16 | MaxLen 按线上实际条目形态收紧：职业领域 120→60、重要关系 120→80、财务概况 120→50、长期目标 120→50（过大易致单条混装多事实，违背原子化） |
| 2026-08-16 | 配额超限不直接截断：反馈 LLM 重生成（最多 2 次，LLM 自行精简/拆分保语义完整），重试仍超限才截断兜底 |
