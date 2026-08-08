# 用户画像功能 — 设计文档

> 版本：v2 | 日期：2026-08-08 | 状态：设计完成，待开发

## 1. 背景与问题

AI 对用户基本事实没有可靠认知底座。如 2026-08-07 对话中助手把用户老家误判为湖南（实际为河南焦作），
根因是 AI 在画像未覆盖的信息上"顺嘴脑补"而非查证。

需要一份用户核心事实画像注入上下文，让 AI 从"猜"变成"读"。

## 2. 设计原则

1. **无感知**：不打断用户，更新全自动，无确认弹窗
2. **防脑补**：只收用户明确陈述的事实，AI 推断不进画像
3. **复用现有机制**：后处理挂 PostChatWorker，注入挂 XMLContext（不破坏 system prompt 前缀缓存）
4. **通用定位**：字段设计不偏向特定职业，适用于任何用户的个人助手场景

## 3. 两层架构

| 层 | 内容 | 维护方式 | 注入方式 |
|---|---|---|---|
| **稳定层**（本次做） | 核心事实（10 个字段） | 对话结束后处理 LLM 提取 | 全量拼入 user 消息 XMLContext，放在最前面 |
| **动态层**（已有） | 所在地/求职阶段/家庭关系状态等 | 复用现有主题机制 | 按需主题检索 |

### 3.1 稳定层字段定义

共 10 个字段，白名单放代码静态配置（`internal/agent/profile_fields.go`）。
每个字段包含两份描述：`ExtractDesc`（指导提取 LLM 如何操作）和 `InjectDesc`（告诉主 LLM 这是什么）。

| # | 字段 | InjectDesc | ExtractDesc 要点 |
|---|------|------------|------------------|
| 1 | 称呼 | 用户偏好的称呼方式 | 仅用户明确指定时才提取，不推断 |
| 2 | 出生年份 | 用户的出生年份 | 仅接受用户明确陈述的年份数字，不从年龄推算 |
| 3 | 籍贯 | 用户的籍贯/老家 | 用户说"我老家xxx的"等表达时提取 |
| 4 | 性别 | 用户的性别 | 仅当用户明确陈述，不推断 |
| 5 | 职业领域 | 用户当前从事的行业和岗位方向 | 不限技术栈，侧重领域+角色 |
| 6 | 重要关系 | 用户生活中重要的人物及其关系 | 格式 "人名: 关系, 简介; ..."，仅收录反复出现的关键人物 |
| 7 | 饮食习惯 | 用户长期稳定的饮食偏好与忌口 | 不是一时口味变化，是长期模式 |
| 8 | 财务概况 | 用户资产/收入/负债的整体轮廓 | 不是具体数字，只提取模糊轮廓 |
| 9 | 长期目标 | 用户当前人生阶段的核心长期目标 | 只提取用户明确陈述的目标 |
| 10 | 补充说明 | 用户的个性化补充信息 | LLM 自行判断是否值得写入，source=llm_additional |

完整描述见第 11 节。

## 4. 总体架构

```
                         ┌─────────────────────┐
                         │    orchestrator      │
                         │  buildUserMessage()  │
                         │    ↓                 │
                         │  profileRepo.        │
                         │    GetByUser()       │
                         │    ↓                 │
                         │  生成 <profile>      │
                         │  packet → XMLContext ─┼──→ LLM 对话上下文
                         └─────────────────────┘

 ┌──────────────┐        ┌──────────────────────┐
 │  onChatEnd   │──────→ │  CronRunner           │
 │  (每次对话后) │        │  NotifySessionEnd()   │
 └──────────────┘        │  ↓                    │
                          │  DelayedTask          │
                          │  ("session_end")      │
                          │  ↓ (debounce 后触发)  │
                          │  PostChatWorkerGroup  │
                          │    ├─ SummaryWorker   │
                          │    ├─ MemoryWorker    │
                          │    ├─ StateWorker     │
                          │    └─ ★ UserProfileWorker (新增)
                          │         ├─ 冷启动: mini ReAct + unified_search
                          │         └─ 正常: Flash ChatSync 单次提取
                          └──────────────────────┘
```

两部分共享一个 `UserProfileRepo`：worker 往里写，orchestrator 往外读。

## 5. Part 1 — 后处理任务（UserProfileWorker）

### 5.1 接口

实现 `PostChatWorker` 接口：

```go
type UserProfileWorker struct {
    flashClient llm.LLMClient
    registry    *tool.Registry
    profileRepo *service.UserProfileRepo
}

func (w *UserProfileWorker) Name() string { return "profile" }

func (w *UserProfileWorker) Process(ctx context.Context, sess *SessionSnapshot) error
```

### 5.2 总体流程

```
Process(sess):
  hasAny = profileRepo.HasAny(userID)
  if !hasAny → runColdStartReAct(userID)
  if hasAny  → extractNormal(userID, sess.Messages)
```

### 5.3 模式 A：冷启动

**触发条件**：`user_profile` 表该用户无任何行。

**执行流程**：mini ReAct 循环，Flash + unified_search 工具，最多 5 轮。

```
runColdStartReAct(userID):
  1. 构建 system prompt（含字段定义 + 工具指引 + 输出格式 + 规则，见 5.3.1）
  2. 初始化 messages: [{role:"user", content:"请搜索并提取我的用户画像"}]
  3. tools = [unified_search 的 native FC 工具定义]
  4. for iter in 0..5:
       resp = flashClient.ChatSync(ctx, req)
       if resp has ToolCalls → registry.ExecuteByName() → 追加 tool result → 继续
       else → json.Unmarshal(resp.Content) → 对照白名单过滤 → Upsert → return
  5. 超时未完成 → log + return（下次 session_end 自动重试）
```

#### 5.3.1 冷启动 System Prompt（纯文本分节）

```
你是一个用户画像提取助手。你的任务是搜索用户的记忆、笔记、
历史对话，了解用户的基本背景信息，然后提取用户画像。

—————— 可用工具 ——————

你只有一个工具：unified_search
- unified_search.semantic_search(query, types, top_k)
  语义搜索。types 可选 memory/note/summary/theme/history，默认全部。top_k 默认 5
- unified_search.keyword_search(keywords, types, topic, top_k)
  关键词搜索。keywords 用空格/逗号分隔。types 同上，topic 仅对 memory/note 生效

建议先做一次宽泛搜索了解用户，再针对需要的字段做精确搜索。

—————— 提取字段 ——————

{遍历 ProfileFields，每个字段输出其 ExtractDesc}

—————— 输出格式 ——————

信息收集完成后，输出纯 JSON（不要用 markdown 代码块包裹）：
{"字段名1":"值1","字段名2":"值2"}

没找到信息的字段直接省略，不要填"未知"、"无"等占位符。
如果完全没有找到任何信息，输出 {}。

—————— 重要规则 ——————

- 只填写用户明确陈述过的事实，禁止用你的推断填充
- 值要简洁，一句话能说清
- 不确定的信息宁可省略，不要猜测
- 你最多有 5 轮搜索机会，合理规划搜索内容
```

### 5.4 模式 B：正常增量提取

**触发条件**：`user_profile` 表已有数据。每次 session_end 都运行。

**执行流程**：单次 ChatSync，无工具。

```
extractNormal(userID, sessionMessages):
  1. 加载已有画像：existing = profileRepo.GetByUser(userID)
  2. 将 session user 消息编码为 XML（<messages> 块）
  3. 构建 system prompt（共享的通用后处理助手，见 5.4.1）
  4. 构建 user message（<profile_extraction> XML，见 5.4.2）
  5. resp = flashClient.ChatSync(ctx, req)
  6. json.Unmarshal(resp.Content) → 对照白名单过滤 → Upsert
```

#### 5.4.1 正常增量 System Prompt（所有后处理 worker 共享，利用前缀缓存）

```
你是一个对话后处理助手。负责对用户对话记录进行批量处理与分析。
输入的消息均为用户角色的发言，按时间顺序排列。
```

（未来 summary/memory/profile 等 worker 共用此 system prompt，仅 user message 不同）

#### 5.4.2 正常增量 User Message（`<profile_extraction>` XML）

```xml
<profile_extraction>
  <task>从以下对话记录中提取用户新画像信息</task>
  <existing_profile>
    <field key="籍贯" updated_at="2026-08-07">河南焦作</field>
    <field key="伴侣" updated_at="2026-06-26">CJ, 自由职业</field>
  </existing_profile>
  <fields>
    <field name="称呼">
      <description>用户希望你如何称呼他/她。可以是昵称、英文名或特定称谓。仅当用户明确说了"你可以叫我xxx"、"叫我xxx就好"等类似表达时才提取。不要从对话中推断称呼偏好。如未找到，省略此字段</description>
    </field>
    <field name="出生年份">
      <description>用户的出生年份，用于推断人生阶段。仅接受用户明确陈述的具体年份（如"我是95年的"），不要从"我今年N岁"推算。值应为4位年份数字字符串，如"1995"。如未找到，省略此字段</description>
    </field>
    <!-- 其余 8 个字段同上，遍历 ProfileFields 的 ExtractDesc -->
  </fields>
  <messages>
    <msg time="08-08 15:30">我老家是河南焦作的</msg>
    <msg time="08-08 15:32">我女朋友叫CJ</msg>
  </messages>
  <rules>
    <rule>只提取用户明确陈述的事实，不要从对话中推断</rule>
    <rule>值要简洁，一句话能说清的事不要展开</rule>
    <rule>与已有画像一致的信息不要重复输出</rule>
    <rule>无法确定的信息宁可省略，不要猜测</rule>
  </rules>
  <output_format>输出纯JSON（不要用markdown代码块包裹），仅包含本次对话中新出现或变更的字段：{"字段名":"新值"}。如果本次对话中没有发现任何新画像信息，输出 {}</output_format>
</profile_extraction>
```

- `<existing_profile>` — 已有画像为空时不输出该块（`omitempty`）
- 用 `encoding/xml` 编码，复用现有 `ctxflow/annotation_encoder.go` 的编码器

### 5.5 写入规则

- **范围**：只扫 user 消息（AI 消息不提取）
- **冲突**：用户明说的新事实与旧值冲突 → 直接覆盖
- **禁止**：AI 推断的事实一律不写入
- **单一通道**：不给 LLM 提供画像修改工具，稳定层只走 post-chat 后处理提取
- **source 值**：
  - `explicit` — 正常提取的用户明确陈述
  - `llm_additional` — "补充说明"字段专用，LLM 自行判断
- **白名单过滤**：不在 `ProfileFields` 中的字段直接丢弃

### 5.6 两模式对比总结

| | 冷启动 | 正常增量 |
|------|----------|-----------|
| 触发条件 | profile 表为空 | profile 表非空 |
| LLM 交互 | mini ReAct（多轮） | 单次 ChatSync |
| 工具 | unified_search（native FC） | 无 |
| system prompt | 专用，纯文本分节（含字段定义 + 工具 + 规则） | 共享："你是一个对话后处理助手..." |
| user message | `"请搜索并提取我的用户画像"` | `<profile_extraction>` XML |
| 字段定义 | system prompt 内 | user message XML 的 `<fields>` 内 |
| 已有画像 | 无 | user message XML 的 `<existing_profile>` 内 |
| 消息来源 | 无（LLM 自己搜） | session user 消息 |
| 输出 | 所有已找到字段的 JSON | 仅新增/变更字段的 JSON |

### 5.7 依赖注入

```go
type UserProfileWorkerConfig struct {
    FlashClient llm.LLMClient
    Registry    *tool.Registry
    ProfileRepo *service.UserProfileRepo
}
```

## 6. Part 2 — 对话中携带（XMLContext 注入）

### 6.1 注入位置

在 `orchestrator.buildUserMessage()` 的 `extraPackets` 中加载画像，放在 XML 最前面。

**渲染顺序**：`profile → summary → reminder → history → memory → note → theme → datetime → notice → user_query`

### 6.2 XML 格式

```xml
<profile description="{InjectDesc 汇总}">
  <field key="籍贯" updated_at="2026-08-07" source="explicit">河南焦作</field>
  <field key="伴侣" updated_at="2026-06-26" source="explicit">CJ, 自由职业</field>
  <field key="出生年份" updated_at="2026-08-01" source="explicit">1995</field>
  <field key="补充说明" updated_at="2026-08-05" source="llm_additional">养了一只猫叫咪咪; 有轻度近视</field>
</profile>
```

### 6.3 注入规则

- 画像为空 → 不注入（不出现 `<profile>` 标签）
- 画像非空 → 全量注入（量小，10 个字段 + 补充说明，不走检索直接全量）
- `source=explicit` → LLM 可视为高置信度
- `source=llm_additional` → LLM 需谨慎对待，可能不够准确

### 6.4 实现点

**`blocks.go`**：新增 `TypeProfile` 类型 + `profileBlock` 结构体 + `buildProfileBlock()` + `ProfilePacket()`。
在 `typeOrder` 最前面插入 `TypeProfile`。

**`orchestrator.go`**：
- `Orchestrator` 结构体加 `profileRepo *service.UserProfileRepo`
- `OrchestratorConfig` 加 `ProfileRepo *service.UserProfileRepo`
- `buildUserMessage()` 中：`o.profileRepo.GetByUser() → 非空则追加 ProfilePacket`

## 7. 数据模型

### 7.1 表结构

```sql
CREATE TABLE user_profile (
  user_id       VARCHAR(36) NOT NULL,
  field         VARCHAR(64) NOT NULL,   -- 白名单字段
  field_value   TEXT NOT NULL,
  source        VARCHAR(16) NOT NULL,   -- "explicit" / "llm_additional"
  updated_at    DATETIME NOT NULL,
  PRIMARY KEY (user_id, field)
);
```

一字段一行，避免全量覆盖、并发冲突、回滚粒度粗。

### 7.2 GORM 模型

```go
type UserProfile struct {
    UserID     string    `gorm:"primaryKey;size:36"`
    Field      string    `gorm:"primaryKey;size:64"`
    FieldValue string    `gorm:"type:text;not null"`
    Source     string    `gorm:"size:16;not null;default:explicit"`
    UpdatedAt  time.Time `gorm:"autoUpdateTime"`
}
```

### 7.3 字段配置（`internal/agent/profile_fields.go`）

```go
type ProfileFieldDef struct {
    Field       string   // DB 字段名
    ExtractDesc string   // 提取 LLM 指引（如何找、怎么写、何时省略）
    InjectDesc  string   // 注入时给主 LLM 的说明（这是什么）
    Type        string   // string / list（预留）
}

var ProfileFields = []ProfileFieldDef{
    {
        Field: "称呼",
        ExtractDesc: `用户希望你如何称呼他/她。
可以是昵称、英文名或特定称谓。
仅当用户明确说了"你可以叫我xxx"、"叫我xxx就好"等类似表达时才提取。
不要从对话中推断称呼偏好。
如未找到，省略此字段`,
        InjectDesc: "用户偏好的称呼方式",
    },
    {
        Field: "出生年份",
        ExtractDesc: `用户的出生年份，用于推断人生阶段。
仅接受用户明确陈述的具体年份（如"我是95年的"），不要从"我今年N岁"推算。
值应为4位年份数字字符串，如"1995"。
如未找到，省略此字段`,
        InjectDesc: "用户的出生年份，用于年龄相关判断",
    },
    {
        Field: "籍贯",
        ExtractDesc: `用户的籍贯或老家。
用户可能会说"我老家xxx的"、"我是xxx人"等。
值应为地名，如"河南焦作"。
如未找到，省略此字段`,
        InjectDesc: "用户的籍贯/老家",
    },
    {
        Field: "性别",
        ExtractDesc: `用户的性别。值为"男"或"女"。
仅当用户明确陈述时提取。不要从称谓（如"我女朋友"）推断性别。
如未找到，省略此字段`,
        InjectDesc: "用户的性别",
    },
    {
        Field: "职业领域",
        ExtractDesc: `用户当前从事的行业和岗位方向。
不限具体技术栈或公司名，侧重"做什么领域、什么角色"。
如"互联网产品经理"、"医学生"、"自由职业（设计）"。
简洁概括即可。如未找到，省略此字段`,
        InjectDesc: "用户当前从事的行业和岗位方向",
    },
    {
        Field: "重要关系",
        ExtractDesc: `用户生活中重要的人物及其关系。
格式: "人名: 关系, 简要介绍; 人名: 关系, 简要介绍"。
如"CJ: 伴侣, 自由职业; 张伟: 同事, 同组后端开发"。
只收录对话中反复出现的关键人物，偶然提及一次的不算。
如未找到，省略此字段`,
        InjectDesc: "用户生活中重要的人物及其关系",
    },
    {
        Field: "饮食习惯",
        ExtractDesc: `用户长期稳定的饮食偏好、忌口或特殊需求。
不是一时的口味变化，而是持续存在的饮食模式。
如"不吃辣, 喜欢面食"、"素食"、"对海鲜过敏"。
只提取用户明确陈述且是长期习惯的信息。如未找到，省略此字段`,
        InjectDesc: "用户长期稳定的饮食偏好与忌口",
    },
    {
        Field: "财务概况",
        ExtractDesc: `用户资产/收入/负债的整体轮廓，不是具体数字。
变化极慢的财务基本面。
如"有房贷, 收入稳定"、"刚工作, 月入中等"。
只提取用户明确陈述的模糊轮廓，不要从消费记录或账单推断。
如未找到，省略此字段`,
        InjectDesc: "用户资产/收入/负债的整体轮廓",
    },
    {
        Field: "长期目标",
        ExtractDesc: `用户当前人生阶段的核心长期目标。
如"计划3年内在北京定居买房"、"备考研究生"。
只提取用户明确陈述的具体目标，不要从对话主题推测。
如未找到，省略此字段`,
        InjectDesc: "用户当前人生阶段的核心长期目标",
    },
    {
        Field: "补充说明",
        ExtractDesc: `用户的个性化稳定信息，不在上述9个固定字段覆盖范围内。
可以是任何值得助手长期记忆的用户特征或情况。
由LLM自行判断是否值得写入，宁缺毋滥。
如未找到，省略此字段`,
        InjectDesc: "用户的个性化补充信息（LLM自动提取，可能不够准确）",
    },
}
```

## 8. Repo 接口

```go
type UserProfileRepo struct {
    db *gorm.DB
}

func NewUserProfileRepo(db *gorm.DB) *UserProfileRepo

// Upsert 插入或更新（user_id + field 唯一约束冲突时覆盖）
func (r *UserProfileRepo) Upsert(ctx context.Context, p *model.UserProfile) error

// GetByUser 返回用户所有画像字段
func (r *UserProfileRepo) GetByUser(ctx context.Context, userID string) ([]model.UserProfile, error)

// GetByUserAndField 查询单个字段的值
func (r *UserProfileRepo) GetByUserAndField(ctx context.Context, userID, field string) (*model.UserProfile, error)

// HasAny 检查用户是否有任何画像行（冷启动判断用）
func (r *UserProfileRepo) HasAny(ctx context.Context, userID string) (bool, error)
```

## 9. 文件清单

| 类型 | 文件 | 说明 |
|------|------|------|
| 新建 | `internal/model/user_profile.go` | GORM 模型 |
| 新建 | `internal/service/user_profile_repo.go` | CRUD |
| 新建 | `internal/agent/profile_fields.go` | 字段配置常量（ExtractDesc + InjectDesc） |
| 新建 | `internal/agent/profile_worker.go` | PostChatWorker（含 mini ReAct 冷启动 + 正常增量） |
| 改动 | `internal/app/init_db.go` | AutoMigrate 加 `&model.UserProfile{}` |
| 改动 | `internal/app/init_repos.go` | `Repos` 加 `UserProfile` 字段 |
| 改动 | `internal/agent/ctxflow/blocks.go` | 加 `TypeProfile` + block 结构体 + builder + `typeOrder` 最前 |
| 改动 | `internal/agent/orchestrator.go` | `buildUserMessage()` 加 profile 加载；加 `profileRepo` 字段 |
| 改动 | `internal/appinit/agents.go` | 构造 `UserProfileWorker` → `sessionEndGroup`；profileRepo → orchestrator |

## 10. 决策记录

- 2026-08-08 v1：稳定层只走「对话后处理提取」单一写入通道，不给 LLM 提供画像修改工具
- 2026-08-08 v1：画像注入不走 system prompt（保护前缀缓存），注入 user 消息 XMLContext 最前面
- 2026-08-08 v1：冷启动用 LLM 自主 ReAct 探索（开放只读工具），而非从全量历史 dump 一次性提取
- 2026-08-08 v1：一字段一行存 DB（不存整份 JSON），避免全量覆盖和并发问题
- 2026-08-08 v2：字段从 9 个扩到 10 个，去掉「原生家庭」「核心人物」「稳定偏好」，加入「称呼」「性别」「重要关系」「饮食习惯」「补充说明」
- 2026-08-08 v2：字段描述分离为 ExtractDesc（提取指引）和 InjectDesc（注入说明），存为常量多方复用
- 2026-08-08 v2：正常增量不做关键词预筛，每次 session_end 都跑
- 2026-08-08 v2：正常增量 user message 使用 `<profile_extraction>` XML 编码，复用 encoding/xml
- 2026-08-08 v2：正常增量 system prompt 设计为通用后处理助手（与 summary/memory 等 worker 共享，利用前缀缓存）
- 2026-08-08 v2：冷启动 system prompt 使用纯文本分节格式，user message 极简
- 2026-08-08 v2：冷启动工具锁定为 unified_search（semantic_search + keyword_search），覆盖全部 5 个搜索域
- 2026-08-08 v2：画像为空时不注入空 `<profile>` 标签
- 2026-08-08 v2：source 值：`explicit`（用户明确陈述）、`llm_additional`（补充说明专用）
