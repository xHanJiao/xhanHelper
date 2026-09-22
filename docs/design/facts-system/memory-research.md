# 记忆系统调研：工作形态 vs 非工作形态，各记住什么

> 日期：2026-09-22 | 状态：调研（第一阶段）
> 目的：盘点主流/同类产品在不同「工作形态」下让记忆系统记录什么、丢弃什么、如何 ADD/UPDATE/DELETE、如何维护与注入，作为事实系统（facts）重定位的依据。

## 0. 结论速览

| 系统 | 面向 | 记什么 | 组织键 | 维护 | 注入 |
|---|---|---|---|---|---|
| **mem0** | 通用个人（非工作） | 偏好/个人信息/计划/健康/职业/杂项；v3 起也抽用户分享的文档与助手建议 | 无分层，扁平事实 + 可选 user_id/agent_id/run_id 命名空间 | 抽取→与旧记忆比对→ADD/UPDATE/DELETE/NONE；v3 改为 ADD-only + 链接去重 | 检索 top-k 回填 |
| **旧 xhanHelper memory** | 通用个人（非工作假设） | 事实/偏好/习惯/计划/观点/反思/决定/经验/价值观变化 | `topic` | 只 append + confirm_count，无修订 | 按检索 |
| **WorkBuddy「关于你的记忆」** | 通用个人 | 指令 / 身份 / 职业 / **项目** / 偏好 | 分类 + `[日期] 条目` | **每晚重新生成摘要**；另有 `update_memory` 工具 create/update/delete | 注入对话 |
| **opencode** | 编码（工作） | 项目指令（AGENTS.md）+ 当前任务工作态 | 项目根文件 | compaction 迭代合并 | 常驻 system prompt |
| **pi** | 编码（工作） | **无长期记忆**；仅 compaction + 分支摘要 + AGENTS.md + skills | — | — | — |
| **minimax-code** | 编码（工作） | 用户画像 / 助手 MEMORY.md / 主题文件 / 每日日志 | **用户级 / 助手级 / 项目级 三层** | 热层有硬上限 + 超限清理归档；每日 TTL | 分层注入（热层常驻、主题按需） |
| **WorkBuddy 本地记忆** | 通用（工作） | `MEMORY.md` 长期记忆正文 + 工作日志 + 技能沉淀 | 本地文件 | 纯正文所见即所得 | 本地注入 |
| **xhanHelper profile（现有，正样本）** | 用户稳定层 | 白名单 10 字段 | 字段 | 每字段全量替换 + 保留最近 3 批 | 稳定层常驻 |

一句话：**"用户是谁"是各类系统的公共能力，做法成熟（mem0 / 画像）；"事到哪了"（工作形态）编码代理做得最细，核心手段是分层 + 有界热层 + 清理归档 + 按需读取；"项目"是两者交集，都记但停在摘要级。**

---

## 1. 非工作形态（用户 / 生活）

### 1.1 mem0（`mem0/configs/prompts.py`）

**抽取类型**（`FACT_RETRIEVAL_PROMPT` / `USER_MEMORY_EXTRACTION_PROMPT`，7 类）：

1. 个人偏好（食物、产品、活动、娱乐的好恶）
2. 重要个人信息（姓名、关系、重要日期）
3. 计划与意图（即将到来的事件、旅行、目标）
4. 活动与服务偏好（餐饮、旅行、爱好）
5. 健康与养生偏好（饮食限制、健身习惯）
6. 职业信息（职位、工作习惯、职业目标）
7. 杂项（喜欢的书、电影、品牌等）

铁律：**只据用户消息**（`GENERATE FACTS SOLELY BASED ON THE USER'S MESSAGES`），不得取助手/系统消息；相对时间锚定到 `Today's date`。

**v3 `ADDITIVE_EXTRACTION_PROMPT`（转向只增）**：
- 明确 **从 user + assistant 都抽**：user 给个人事实/偏好/计划；assistant 给「给过的建议、制定的计划、查到的信息、达成的约定」，但以用户语境表述（"User was recommended X"）。抽用户分享的文档/资料里的关键事实、第一次/里程碑、吃了什么+谁在场、动机与启发。
- 明确**不抽**：助手的模糊评价（"you seem passionate"）、通用应答（"Sure!"）、助手对自己能力的元评论。
- 去重靠「Recently Extracted Memories（本 session 已抽，最多 20 条）」+「Existing Memories」；相关则写入 `linked_memory_ids`，不再改旧记忆。

**ADD/UPDATE/DELETE**（`DEFAULT_UPDATE_MEMORY_PROMPT` + `get_update_memory_messages`）：
把「当前记忆库」+「新抽事实」一起给 LLM，输出：

```json
{"memory":[{"id":"...","text":"...","event":"ADD|UPDATE|DELETE|NONE","old_memory":"..."}]}
```

规则：库里空则全 ADD；UPDATE 保留原 id、只改值；DELETE 移除；无变化 NONE。v3 已弱化这套，改为「只增 + 链接」，把「改」的责任交给后续整理。

### 1.2 旧 xhanHelper memory（`server/internal/agent/extractor.go`）

- 抽取目标（`buildMemoryInstructionXML`）：「用户的事实/偏好/习惯/计划、反复讨论的话题、观点和判断、分享的外部信息、反思/自我剖析/复盘、重大决定及理由、从经历总结的经验教训、偏好或价值观的变化」，**合并同类、每次最多 3 条**。
- 产出：`topic + tags + summary + facts[]`，进 `user_memories`。
- 去重：`content_hash` 精确 + 向量 0.85（`isDuplicate`）。
- **无修订能力**：只 append + `confirm_count`（旧系统的已知短板）。

### 1.3 WorkBuddy「关于你的记忆」

- 位置：`/Applications/WorkBuddy.app`（Tencent CodeBuddy 系，Electron/asar，v5.3.5）。
- 文案：「记忆让 {name} 记住你的偏好和习惯，对话越多，它就越懂你。」
- **分类由导入模板定死**（`settings.memory.modal.import.promptTemplate`），按顺序输出：

  1. **指令**：明确要求遵循的规则——语气、格式、风格、"始终做 X"、"绝不做 Y"、对助手行为的纠正。仅整理可明确识别且客观存在的规则，不脑补。
  2. **身份**：姓名、年龄、所在地、教育背景、家庭、人际关系、语言能力、个人兴趣（仅用户主动分享的非敏感信息，不输出证件号/联系方式/账号）。
  3. **职业**：当前和过往的职位、公司、主要技能领域。
  4. **项目**：实际参与构建或投入精力的项目，**每个项目一条，含项目功能、当前状态、关键决策**，以项目名或简短描述开头。
  5. **偏好**：广泛适用的观点、品味、工作风格偏好。

  格式：`[YYYY-MM-DD] - 条目内容`，按日期从早到晚；未知日期写 `[unknown]`。

- **机制：每晚重新生成摘要**（`settings.memory.modal.manage.desc`："这是 {name} 记住的关于你的信息，该摘要每晚重新生成。"），可查看/编辑/重置/提交修改建议，支持「从 ChatGPT、Claude 导入」。
- **`update_memory` 工具**（agent 可调）：`action: create|update|delete`，字段 `title` / `knowledge_to_store` / `existing_knowledge_id`（update/delete 时必需）。另有 `create_memory` / `delete_memory`。
- 记忆里的「内存记忆」是按助理独立配置（`colleagues.imConfig.memory`："为每位助理独立配置长期记忆"）。

> 关键观察：WorkBuddy 把**「项目」放进了"关于你的（用户）记忆"**里，但停在**一条一行、带日期的摘要级**（功能/状态/关键决策），不记执行细节。这正好说明"工作内容"与"用户画像"的边界不是"两张表"，而是**粒度**。

---

## 2. 工作形态（编码 / 研讨 / 执行）

### 2.1 opencode（`/Users/you/xhanCodes/opencode-dev`）

- **无语义记忆工具**。工作记忆 = 两样东西：
  1. **项目指令 `AGENTS.md`**（`packages/opencode/src/session/instruction.ts` 加载；minimax 侧的 `project/instructions.ts` 注释声明："IMPORTANT: These instructions OVERRIDE any default behavior"）。人写、随仓库走。
  2. **compaction 摘要**（`packages/core/src/session/compaction.ts`）——上下文将满时把旧消息压成固定模板的工作态：

     ```
     ## Objective
     ## Important Details   （约束/偏好/决定及原因/关键事实假设）
     ## Work State
     ### Completed / ### Active / ### Blocked
     ## Next Move
     ## Relevant Files
     ```

     迭代合并：新对话覆盖旧摘要冲突处；已完成从 Active 挪到 Completed。
- 另有会话消息 JSONL 持久化。

### 2.2 pi（`/Users/you/xhanCodes/pi`）

- **确认无长期记忆系统**（`packages/coding-agent/docs/` 无 memory 文档；`memory-storage.ts` 是会话存储）。
- 只有：**compaction**（`docs/compaction.md`：结构化摘要 + 累计跟踪文件操作）、**分支摘要**（`/tree` 切换时保留上下文）、`AGENTS.md`/context files、`prompts`/`skills`（`.pi/prompts`、`.pi/skills`）。

### 2.3 minimax-code（`/Users/you/xhanCodes/minimax-code`）—— 工作形态最完整的参考

**分层架构**（`packages/shared/src/memory-limits.ts`）：

| 层 | 文件 | 注入 | 上限 |
|---|---|---|---|
| 用户层 | `memory/user.md` | `<user_profile>` 常驻 | 尾部截断 |
| 助手层（热层） | `agents/<name>/memory/MEMORY.md` | `<agent_memory_tail>` 尾部 | 15KB 软 / 20KB 硬，超 18KB 触发清理 |
| 索引层 | `.summary.md` | `<agent_memory_summary>`（行号索引，供按行读全文） | 4KB |
| 主题层 | `memory/<topic>.md` | `<available_memory_topics>` 只注入元数据 | 最多 10 个 × 30KB |
| 日志层 | `daily/<date>.md` | `<daily_digest>` 近 8 天（近 2 天全量、更早压平） | 60 天 TTL |

**写哪一层：三问测试**（`packages/agent-modules/system-reminder/src/blocks.ts`）：

```
1. 换用户结论会变？     → User Memory（memory/user.md）
   例："MR 不可自动 merge"、"飞书通知群"、"序号指代习惯"、"沟通风格"
2. 换项目结论仍成立？   → Agent Memory（agents/<name>/memory/MEMORY.md）
   例："CI failed 先看日志"、"vitest CI timeout 配置"、"非交互 shell 用 .zshenv"
3. 只在当前项目成立？   → Project Memory（AGENTS.md 或 topic 文件）
   例："MR target branch 是 dev"、"飞书群 chat_id"、"项目级 hook 约定"
```

- **技能优先于记忆**：可复用的新模式走 skill 通道（`mavis skill proposal`），**不写记忆**（`blocks.ts` memory-skill-reminder）。
- **用户画像禁列**（`preparation/prompt-blocks.ts` `buildUserProfileBlock`）：

  > The profile is durable context, not an activity log.
  > Do NOT store: sensitive guesses, psychological labels, one-off moods, raw logs, transient task status, or routine recaps.

- **清理是一等公民**：超硬限 → 清理会话；移除条目**先归档到 `memory/archive/` 再删**；清理有 24h 去重窗口，避免风暴；MEMORY.md 低于 10KB 时跳过清理（无意义）。

### 2.4 WorkBuddy 本地记忆

- **`MEMORY.md`**「AI 的长期记忆记录」，**纯正文、所见即所得**（`local-memory-files.ts` 注释：与 `SOUL.md` 不同，"不做 frontmatter / 标题结构包袱"）。路径 `~/.workbuddy/`（或 `WORKBUDDY_CONFIG_DIR`）。
- **「本地技能与记忆沉淀」**（`settings.agent.localSkillsMemory.description`）：*"自动记录本地记忆、工作日志，自动沉淀和优化技能。数据本地存储，仅在你的设备和工作区中保留。"*
- 身份三件套：`IDENTITY.md`（AI 名）、`USER.md`（对你的称呼）、`SOUL.md`（人设/人格）。
- 「本地记忆」开关（`settings.memory.localMemory`）：*"将日常工作记忆写入到本地文件，跨会话保留上下文。"*

---

## 3. xhanHelper profile（现有，正样本）

`server/internal/profilefield/profilefield.go` + `server/internal/agent/profile_worker.go`

| 字段 | 形态 | 说明 |
|---|---|---|
| 称呼 | single | 明确指定的称呼 |
| 出生日期 | single | 明确陈述，不推算 |
| 籍贯 | single | 老家地名 |
| 性别 | single | 明确陈述，不从称谓推断 |
| 职业领域 | list(6) | 行业/岗位方向与履历条目 |
| 重要关系 | list(10) | 反复出现的关键人物 |
| 饮食习惯 | list(5) | 长期稳定偏好/忌口 |
| 财务概况 | list(5) | 资产/收入/负债轮廓，不从账单推断 |
| 长期目标 | list(5) | 明确陈述的目标 |
| 补充说明 | list(10) | 兜底杂项，宁缺毋滥 |

设计属性（值得复用）：

- **白名单即代码**：新增字段 = 改代码 = 人工放行。
- **配额在字段级**：单条 `MaxLen`、字段 `MaxFacts`，越限反馈 LLM 重生成（`quotaRetryMax`=2），耗尽截断兜底。
- **更新语义**：增量提取只输出「有变化或需修订的字段」，每字段输出**完整事实列表**（保留旧事实原文、省略已过时的）；写入 `user_profile_facts` 时**整字段替换**（旧值标 `outdated`）。
- **维护**：`CleanupOutdated` 保留最近 3 批，更旧物理清理。
- **单一写入通道**：只走 profile worker，**不向对话 LLM 提供画像修改工具**。
- **注入**：作为稳定上下文进 system prompt（缓存友好）；`InjectDesc` 告诉主 LLM 每个字段是什么。
- **冷启动**：MiniReAct + `unified_search`（Pro 模型）从零建画像。

---

## 4. 横向对比提炼

### 4.1 「用户是谁」——公共能力，做法已收敛

多个系统的用户画像内容高度一致：**身份（姓名/年龄/地/教育/家庭/关系）、职业、偏好、长期计划、健康**。WorkBuddy 多一栏**「指令」**（对助手行为的规则：始终做/绝不做/纠正），minimax 多一条**「沟通风格/指代习惯」**。xhanHelper profile 目前**没有"行为指令/协作偏好"这一类**。

### 4.2 「事到哪了」——编码代理最细，三招通用

1. **分层**：按「换什么结论会变」分用户级 / 助手级 / 项目级（minimax 三问）。
2. **有界热层 + 冷层按需**：常驻层有硬上限，超限归档；大内容放主题/文件，用时才读（opencode 的 Relevant Files / minimax 的 topics）。
3. **结构是"工作态"**：Objective / 已完成 / 进行中 / 阻塞 / 下一步 / 相关文件（opencode 模板）——**没有一处记环境路径、命令、报错原文**。

### 4.3 维护是一等公民

| 系统 | 维护手段 |
|---|---|
| mem0 | 抽取→比对→ADD/UPDATE/DELETE；v3 改 ADD + 链接 |
| 旧 xhanHelper | 无（只 append） |
| WorkBuddy | 每晚重新生成摘要（整体重写） |
| minimax | 热层上限 + 清理 cron + 归档 |
| opencode/pi | compaction 迭代合并 |
| xhanHelper profile | 每字段全量替换 + outdated 批审计清理 |

### 4.4 「项目」是两类形态的交集

mem0（"Discussed the new project"）、WorkBuddy（「项目」分类：功能/状态/关键决策）、minimax（助手级 memory 与项目级 memory）**都记项目**，但都停在**摘要级**（一条一行、带日期/状态），**不记执行细节**（路径、命令、代码）。这与 xhanHelper 的定位（只研讨、不执行）高度吻合。

---

## 5. 对 xhanHelper 的启示

1. **两形态的边界是粒度，不是表**：用户画像可容纳"在做哪些项目 + 状态 + 关键决策"（摘要级）；执行细节（本地路径、下载了什么、命令、报错）**任何一层都不该记**。
2. **分层判据用 minimax 三问**，比"生活/工作"二分更可操作：换用户才变 → profile；换项目才变 → 工作层；只此项目 → 项目文件/AGENTS。
3. **"禁记清单"比"该记清单"更能压噪声**：敏感猜测、心理标签、一次性情绪、原始日志、临时任务态、例行复述、执行环境态。
4. **热层必须有涯 + 冷层按需 + 清理归档**——这正是 facts 与旧 memory 共同的窟窿，而 profile 已经做对。
5. **profile 的四个属性可复制到工作层**（单一写入通道 / 全量替换式更新 / 有界 + 清理归档 / 稳定注入），但**白名单字段不可复制**——用户身份有界可枚举，工作/项目开放无界，只能用**项目/话题做键**。
6. **「行为指令/协作偏好」目前无家可归**：facts 里最真的那条（要求结论先行）属这一类，profile 无此类字段（workbuddy 有「指令」类）。

## 6. 待决问题（进入设计前）

1. 「行为指令/协作偏好」并入 profile 新增字段，还是单开一层？
2. 工作层的键用「项目」（WorkBuddy 式，稳）还是「话题」（minimax topic 式，细）？
3. `facts` 表去向：改造成工作层（保留 ADD/UPDATE/DELETE 机制），还是废弃，由 profile（用户）+ 工作层（项目）+ note 三线承接？
4. 维护策略：走 mem0 的增量更新，还是 WorkBuddy 的"定期重生成"？

## 附录 A：出处

- mem0：`/Users/you/xhanCodes/mem0/mem0/configs/prompts.py`（`FACT_RETRIEVAL_PROMPT` / `USER_MEMORY_EXTRACTION_PROMPT` / `DEFAULT_UPDATE_MEMORY_PROMPT` / `ADDITIVE_EXTRACTION_PROMPT`）；`mem0/memory/main.py`（调用点）。
- opencode：`/Users/you/xhanCodes/opencode-dev`（`packages/core/src/session/compaction.ts`、`packages/opencode/src/session/instruction.ts`、`AGENTS.md`）。
- pi：`/Users/you/xhanCodes/pi`（`packages/coding-agent/docs/compaction.md`、`docs/*`）。
- minimax-code：`/Users/you/xhanCodes/minimax-code`（`packages/shared/src/memory-limits.ts`、`packages/local-runtime-v2/src/service/turn-system/agent-host/preparation/{prompt-blocks.ts,config/memory-prompt-composer.ts}`、`packages/agent-modules/system-reminder/src/blocks.ts`、`packages/local-runtime/src/memory/*`、`packages/agent-tools/src/desktop/builtin-defs.ts`）。
- WorkBuddy：`/Applications/WorkBuddy.app/Contents/Resources/app.asar`（i18n 文案、`settings.memory.modal.import.promptTemplate`、`update_memory` 工具 schema、`local-memory-files.ts`）。
- xhanHelper profile / memory / facts：`server/internal/profilefield/profilefield.go`、`server/internal/agent/{profile_worker,extractor,fact_extractor,fact_worker,post_chat_group}.go`、`server/internal/agent/promptbuilder/stable_context.go`。

## 附录 B：关键原文摘录

minimax 用户画像禁列（`prompt-blocks.ts`）：

> `<user_profile>` … The profile is durable context, not an activity log.
> Use the native `memory` tool for updates; do not edit this injected block directly.
> Do NOT store: sensitive guesses, psychological labels, one-off moods, raw logs, transient task status, or routine recaps.

opencode compaction 摘要模板（`compaction.ts`）：Objective / Important Details / Work State(Completed·Active·Blocked) / Next Move / Relevant Files；合并规则"新对话比旧摘要新，冲突处对话胜"。

mem0 更新格式（`get_update_memory_messages`）：

> `{"memory":[{"id":"<ID>","text":"<Content>","event":"ADD|UPDATE|DELETE|NONE","old_memory":"<Old>"}]}` … UPDATE 的 ID 保持不变、只改值；DELETE 移除键值对。

WorkBuddy 导入画像模板类别（`settings.memory.modal.import.promptTemplate`）：指令 / 身份 / 职业 / 项目（含功能、当前状态、关键决策）/ 偏好；格式 `[YYYY-MM-DD] - 条目`。
