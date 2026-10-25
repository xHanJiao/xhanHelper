# 联网工具（web search / fetch）· 人话版方案

> 范围：把 DeepSeek 官方的 Search / Fetch 接口接成一个服务端工具组 `web`，让**主循环**（不是只靠子 agent）能自己联网。
> 估算（开工）：8 面 / 2 缝（DeepSeek 引擎效果、`expose_tools` 目录暴露）/ 1 次真机往返 / 2 个待拍决定 · ~1.5–2.5h
> 估算（定稿）：10 面 / 2 缝（1 条需真机探针：DeepSeek 搜索质量）/ 1 次真机往返 / 0 个待拍决定 · ~1.5–2h

## 一、问题：现在联网这条路太绕

今天主循环**一点联网能力都没有**。想查网页只能派 `task` 子任务 `web_explore`，而它：

- **必须 macOS 客户端在线**（`requiresClient: true`，拉的是客户端浏览器工具）。Web 端、或者 Mac 没开着 → 直接不可用。
- 成本高：子 agent 最长 600 秒、10 轮，为了查一条新闻要跑一整套循环。
- 浏览器抓回来的正文，历史上还因为「单页 5 万字符全量进上下文」被从白名单里踢掉过（`docs/design/phase-18-subagent-explore.md`）。

也就是说：**「顺手搜一下」「读一页原文」这种最轻的联网需求，现在付的是最重的代价。**

## 二、DeepSeek 给了什么

两个纯 HTTP 接口，跟模型调用同一个 key、同一个域名：

| 接口 | 干什么 | 返回 |
|---|---|---|
| `POST /v1/tools/search` | 按 query 搜网页 | `results[]`：标题、URL、短摘要、**长摘要（600–800 字，官方注明"给模型读"）**、可选正文、发布时间、站点名、权威性 0–4、排序分 0–3 |
| `POST /v1/tools/fetch` | 抓一个网页 | `{title, content}`，`content` 是**清洗好的 Markdown** |

几个决定方案走向的细节：

- **搜索语法白送**：`site:` / `-site:` / `-词` / `"精确短语"` / `lang:` / `after:` / `before:`。不用我们做筛选参数，模型写在 query 里就行。
- **计费按成功次数**：各 5 元 / 1000 次。`count` 传多少都是同价 —— 所以**多要几条不额外花钱，只多花 token**。
- **限速**：Search 100 QPS、Fetch 25 QPS（账号级），个人用根本碰不到。
- **拿不到正文就报错**：Fetch 遇到 JS 渲染页、登录墙、被墙 → 404，不返回半成品。
- **API 侧的 `tools` 只支持自定义 function**，没有「服务端内置搜索」这种现成开关 —— 必须我们自己包一层。

## 三、方案：一个工具组，两个函数

```
web.search(query, count?, need_content?)   → 结构化结果（默认只给长摘要）
web.fetch(url)                             → {title, content}
```

**为什么合成一组**：两者永远一起用（先搜再读），分组才能一次 `expose_tools` 都拿到；分成两个组只会多一次暴露往返。

**参数刻意做窄**：

- 不暴露 `ori_question`（`ExecuteContext` 里没有原始问题，为它加字段不值）和 `localization`（本地版没有"用户 IP"这回事）。
- 站点/时间/语言过滤交给搜索语法，不另做参数。
- `need_content` 默认 `false`：默认走长摘要（600–800 字/条，模型够用），要读原文就让模型对选中的 URL 调 `fetch`。这样既省 token，也逼出「先挑再读」的纪律。

**返回瘦身**：只给 `title / url / summary / date / site_name`，丢掉 `snippet`（前端展示用）、`site_icon`、`rerank_score`、`authority`。

**暴露方式**：走现有渐进式暴露，常驻目录里只多一行：

```
web — 联网。搜索网页 / 抓取网页正文。（search, fetch）
```

**只读**：两个函数都是 `ReadOnlyFuncs`，plan 模式可直接用。

**截断**（这里必须说清楚，容易搞反）：

- 现状是**当轮不截、历史才截** —— 工具结果原样进 `role=tool`，只有历史重放时按 `max_tool_result_chars`(20000) 砍。
- 所以一次抓回 5 万字符，**当轮就真的喂 5 万字符**进上下文。
- 结论：**上限得由工具自己兜**。`fetch` 正文按 `max_content_chars`（默认 8000 **字符**，按 rune 截，不会切坏中文）截；search 结果的总预算用 `search_max_chars`（默认 16000），超了就不再追加后续条目，并在返回体写清「只返回前 X/Y 条」。注意 `max_content_chars` 要 ≤ 20000，否则历史回放还会再砍一刀（前后不一致）。

**重试**：429 / 408 / 5xx 自动重试一次（800ms 退避），其余 4xx 不重试，直接把原因报给模型。错误文案要具体：402 →「DeepSeek 余额不足」，403 →「被安全策略拒绝」，404 →「抓不到正文（可能是 JS 渲染页/登录墙），可派 web_explore 用浏览器打开」。

## 四、额度：每天 1000 次，超了转 web_explore

花钱的东西必须有闸。目标不是省钱（1000 次也就 5 元），是**防模型在一次任务里把搜索当循环用**。

**口径**：

- 一个**共享日额度**，search 和 fetch 都算 1 次（单价一样，分开计没意义）。
- **按账号全局**计，不分用户 —— 跟计费粒度一致。
- 默认 `1000` 次/天，`0` = 不限制。

**实现：复用仓库里已有、但已经没人用的 `SearchUsageRepo`**（`search_usages` 表一直在 AutoMigrate 里，Bocha 下掉后这个 repo 就孤立了）。

- `provider = "deepseek_web"`、`total_quota = daily_limit`、`expires_at = 明日 00:00`。
- 它天生适配「按天滚动」：`expires_at` 一变就把 `used_count` 清零（`service/search_usage_repo.go:37`）；递增是 `UPDATE ... WHERE used_count < total_quota`，并发安全。
- 代价很小：`SearchUsage` 重新加回 `app.Repos`（2 行），工具侧用一个小接口注入，不直接依赖 service 包。

**超限后怎么办**（两级，不做「工具内部自动派子任务」——那会把 tool 和 agent 耦死）：

1. `web.*` 返回失败，文案自带下一步动作：

   > 今日联网额度已用完（1000/1000，明日 00:00 重置）。不要再重试；需要继续联网请派 task 子任务（subagent_type=web_explore，需 Mac 客户端在线）。

2. 行为准则第 8 条补一句：`web 工具报额度用尽 → 不再重试，改派 web_explore；客户端不在线则如实告诉用户今日额度已用完`。

**已知取舍**：计数是**请求前预扣**（复用现成原子递增，不为它加新方法），所以**失败的请求也占额度**。比 DeepSeek「成功才计费」保守一点，1000 的量级下可以接受；代码里留一行注释说明。

## 五、配置

```yaml
web_tools:
  enabled: true
  api_key: ""                 # 空则回退 llm.api_key（仅 provider=deepseek 时）
  base_url: https://api.deepseek.com
  timeout_seconds: 30
  search_count: 8             # 模型不传 count 时的条数；模型传值须在 1..20
  max_content_chars: 8000     # fetch 正文 / search need_content 的单条上限
  search_max_chars: 16000     # search 结果总预算（字符），超出则截条并在 note 说明
  daily_limit: 1000           # 每日 search+fetch 合计上限；0=不限制
```

- `count` 契约：不传 = 默认 8；显式 `count=0` / 负数 → **报错**（不静默按默认）；`>20` 夹取并在返回体回显 `requested_count` / `returned_count`。
- `recent` 参数（`day/week/month/year`）：自动换算成 `after:YYYY-MM-DD` 追加进 query，用于「本月/最近/今年」这类时效问题（搜索器不会自己把「10月」理解成本年）；返回体始终带 `today`。
- 结果总预算受 `search_max_chars`（16000）约束，被截时 `note` 写明「只返回前 X/Y 条」，给出的建议只能是模型真能做的（减少 count / 关 need_content），不指向服务端配置项。

- 启动时 `enabled` 但 key 为空（且 provider 不是 deepseek）→ `log.Printf` 说明原因并**不注册**，不 panic：这是可选能力，跟当年 `webfetch.enabled` 同一种降级方式。
- `config.dev/prod/local` 都不用改，key 已经在 `LLM_API_KEY` 里。

## 六、模型怎么知道什么时候用

只改两处 prompt，不加新机制：

- 行为准则第 8 条：改成「单次检索 / 单页阅读**直接**用 web 工具；**多页深挖、需要点击和 JS 渲染**才派 `web_explore`」。
- `task` 工具说明里的「何时使用」：`web_explore` 那行限定为「JS 渲染页面 / 需要交互的多页调研」。

## 七、顺手删掉旧 `webfetch`

`tool/webfetch.go` 现在是**彻底死代码**：默认 `enabled: false`、`HideTool`、`web_explore` 白名单里也早被移除了。新 `fetch` 严格更好（返回 Markdown，而不是自己 `html2text` 剥标签）。

删除：`tool/webfetch.go` + `webfetch_test.go` + `WebFetchConfig` 及其默认值 + 注册块 + `conf/config.yaml` 的 `webfetch:` 段 + 两个测试里当 mock 用的 `"webfetch"` 字符串（改名 `profile`）。约 −350 行。

## 八、改动清单

| 文件 | 动作 |
|---|---|
| `server/internal/tool/web.go` | 新增：工具本体（search / fetch / 额度 / 截断 / 重试） |
| `server/internal/tool/web_test.go` | 新增：httptest 单测 |
| `server/internal/config/config.go` | `WebToolsConfig` + 默认值 + key 回退；删 `WebFetchConfig` |
| `server/conf/config.yaml` | 加 `web_tools:`；删 `webfetch:` |
| `server/internal/app/init_repos.go` | `SearchUsage` 接线 |
| `server/internal/appinit/tools.go` | 注册 `web`（key 缺失时跳过并打日志）；删 webfetch 注册 |
| `server/internal/tool/seeder.go` | 种子加 `web`（Category `search`） |
| `server/internal/agent/promptbuilder/static.go` | 准则第 8 条改一句 |
| `server/internal/tool/task.go` | `web_explore` 定位补一句 |
| `server/internal/tool/webfetch.go`、`webfetch_test.go` | 删除 |
| `server/internal/tool/registry_hidden_test.go`、`expose_tools_test.go` | mock 名 `webfetch` → `profile` |
| `AGENTS.md` | 工具表加 `web`、删 webfetch 行 |
| `docs/design/phase-18-subagent-explore.md` | 尾部追加「后续变更」 |

**不动**：前端、Swift 客户端。`tool_call` / `tool_result` 事件、工具可视化、activity 埋点（`tool_name=web__search`）全现成。

## 九、怎么验证

**单测**（httptest，不打真接口）：

- search 成功：断言请求体里的 `query / count / need_content`，断言返回字段已瘦身。
- search 空结果、`count` 越界夹取、query 超 128 字符截断。
- 429 重试一次后成功；402/403/404 的错误文案。
- fetch 成功、超长截断、非法 URL（无 host / 带 `user:pass` / 非 http）。
- 额度：未超时正常调用且计数递增；耗尽时返回失败文案且**一个 HTTP 请求都不发**（用 httptest 计数器断言）。

**真机**：`make dev` 后让它「搜一条今天的新闻并读一页原文」，检查：目录里能看到 `web`、能按需暴露、工具结果体积合理、日志无报错。

**验收门槛**：`go build ./... && go test ./...` 全绿 + 真机跑通一次。

## 十、风险 & 明确不做

**风险**

- Search 是 DeepSeek 自研引擎，中文网页覆盖/时效我没法离线验证，得真机跑两天看效果；不行就把 `enabled` 关掉。
- `fetch` 对 JS 渲染页无效（错误文案会引到 web_explore）。
- 成本中心（`llmcost`）按 token 记账，**看不到这部分按次费用**，v1 只打日志。

**不做**

- 不接 `ori_question` / `localization`。
- 不把额度余量塞进每次结果（只在超限时报数）。
- 这版不动 `web_explore` 的服务端白名单（子 agent 单条结果预算只有 2000 字符，`fetch` 内容会被截得很狠，要动得连着调 `maxToolResult`）。
- 不接成本中心。

## 后续变更

### 2026-10-25 第一轮真机回归后的修复

回归结论：20 次成功 / 6 次失败，约 0.10 元；search 可用但并发有问题，fetch 需要修分类。已按下列方案修完（同一分支 `feat/web-tools`）。

**1. 并发首次调用撞 `search_usages` 唯一索引（1062）**
- 根因：`TryUse` 是 `First`（无记录）→ `Create`，两个并发请求会同时插入。不是索引建错——这张表是**单行滚动计数器**，`provider` 唯一索引是对的；该修的是插入幂等。
- 改法：`Create` 加 `clause.OnConflict{DoNothing: true}`（MySQL 走 `ON DUPLICATE KEY UPDATE id=id`，SQLite 走 `ON CONFLICT DO NOTHING`），随后重读真实行再原子递增。
- 顺带：跨天重置改成带旧值条件的 `UPDATE`（并发下只有一个请求真正清零），`atomicIncrement` 回读真实计数（原先返回的 `used` 可能是并发前的旧值），并补上原先没检查的 `Update` 错误。
- 额度失败不再把 DB 细节（表名/索引/SQL）喂给模型，只进日志。
- 测试：`service/search_usage_repo_test.go`（并发首插、超限、跨天重置、跨天与递增并发、DB 错误上抛）。

**2. fetch 失败分类（原先全部糊成「抓不到正文（404）」）**
- 上游 404 只给一句 `failed to fetch URL (request_id: ...)`，没有类型信息；现在失败后**本地探一次**目标站点（DNS → `GET Range: bytes=0-0`，5s）来分类：`dns_error / timeout / blocked / unsupported_content / not_found / target_error / upstream_only`（本地能通、抓取服务侧失败）。
- 失败结果除人类文案外，`Data` 里带 `{stage, api_status|target_status, reason, detail, request_id}`，模型可据此决定「换来源 / 稍后重试 / 放弃」。
- **SSRF 防护**：本地探测前解析 host，命中回环/私网/链路本地/组播/未指定地址直接判 `private_address`，避免被网页注入后用联网工具扫内网。
- **200 + 错误页**：上游对 SPA 软 404 会返回 200（实测 api-docs 死链返回外壳 + `# Page Not Found`，319 字符）。现在「正文 <800 字符且命中 not-found 文案」判为失败，`reason=error_page`。

**3. Markdown 转义还原（最伤语义的一条）**
- 上游清洗器把 CommonMark 可转义标点全转义：GitHub API 回来是 `\{\"id\"\: 908531752\,`，文档回来是 `\# 标题`、`\|`。现在 `unescapeMarkdown` 按 CommonMark 规则还原（`\` + ASCII 标点 → 标点），作用于 fetch 的 `title/content` 与 search 的 `content`；`summary` 本来就干净，不动。
- 取舍：代码块里本想显示字面 `\.` 的会被还原成 `.`。

**4. search 结果清洗（基础版）**
- 丢空标题、按清洗后 URL 去重；
- URL 剥跟踪参数（`utm_*`/`gclid`/`fbclid`/`spm`/`scm`/`share_token`/`partner`/`aff`/`affiliate`/`p_source`/`p_type`/`via`）；
- `rerank_score < 1.0` 且剩余 ≥3 条时丢弃（官方说 ≥1.5 为优质，留 1.0 作底线）；
- `count` 边界写进工具描述（默认 8、最多 20、非正数按默认）；`date` 标注「上架/索引时间，可能不准，别当发布时间下断言」。
- 实测确认 API 没有 10 条上限（`count=10/20/30` 分别返回 10/20/30 条），上限是我们自己收的。

**5. 维持不变的口径**
- 额度仍是**请求前预扣**，失败请求也占额度（6 次失败各扣 1 次）；DeepSeek 侧失败不计费。
- 不做本地兜底抓取：实测本机连维基都超时，兜底救不了这类站点（上游失败多为其网络受限），只多一套要维护的抓取器。
- 不做「每域名最多 N 条」：会藏掉有用结果，先靠低分过滤与去重。

**已知局限**：`fetch` 的成功率受上游网络能力限制（维基等站点上游拿不到，本地也拿不到）；`error_page` 与低分过滤都是启发式，可能误伤极短页面或长尾查询。

### 2026-10-25 第二轮真机回归后的修复

回归结论：转义、404 分类、URL 清洗均已验证通过；剩余三条 + 一条新问题，处理如下。

**1. `upstream_status` 语义拆开**
- 原字段混装了「DeepSeek 接口状态」和「目标站状态」，dns/PDF/超时这三类本地失败也顶着 404，按状态码分支会误判。
- 现在拆成：`api_status`（DeepSeek 接口状态，连不上 DeepSeek 时省略）+ `target_status`（本地探测拿到的目标站状态，DNS/超时/私网时省略）+ `reason` / `detail` / `request_id`。
- 本地探测函数改为同时返回状态码。

**2. `count` 边界与预算说明**
- 纠正一个误判：不是「clamp 到 10」。API 实测 `count=10/20/30` 分别返回 10/20/30 条；只剩 10 条是 **token 预算**（旧值 `max_content_chars=8000`）截掉的。
- 现在：`count` 不传 = 默认 8；显式 `count=0`/负数 → **报错**；`>20` 夹取并在返回体回显 `requested_count` / `returned_count`。
- search 结果总预算独立成 `search_max_chars`（默认 **16000**）；被截时 `note` 写明「只返回前 X/Y 条；如需更多请减少 count 或关闭 need_content」（不给模型指向服务端配置项的建议），不再只给一个 `truncated: true`。

**3. 时效性（只做参数层）**
- 加 `recent` 参数（`day/week/month/year` → 自动换算 `after:YYYY-MM-DD` 追加进 query；query 里已有 `after:` 就不重复加）；返回体始终带 `today`；工具描述要求时效类问题必须带 `recent`。
- 上下文本来就注入了当前日期（`<datetime>` 块），但模型没被告知「相对时间要自己转 absolute」，这次在工具描述里说清。
- 不做 `ori_question`（本轮决定只做参数层）、不按 `date` 重排（date 实测不可靠，脏日期重排会更糟）。

**4. 并发（MySQL 实测）**
- 加门控压测 `service/search_usage_mysql_stress_test.go`（`QUOTA_MYSQL_DSN` 未设置时 Skip，CI 不跑，也不调任何付费接口）：
  5 轮 ×（100 并发首插 + 50 并发递增）对 dev MySQL `xhan_helper_test` 执行，**零重复键、行数恒为 1、`used_count` 精确 = 150/轮**。

### 2026-10-25 第三轮真机回归后的修复（收敛版）

时效不做（用户决定可容忍，`recent` 保留为可选参数，默认行为不变）。只修两处：

**1. 失败字段定稿：`stage` + 状态码可见性规则**
上一版只把 `upstream_status` 改名成 `api_status`，语义没定清：DNS/超时照样占位 404；PDF 出现「`api_status:404` + `target_status:206`」自相矛盾（206 是本地 `Range` 请求的产物）；`error_page` 又给出 `api_status:200`（成功码出现在失败结果里）。

现在 `stage` 显式说明结论来自哪一层，状态码只在它真是失败原因时出现：

| stage | 含义 | 允许出现的状态码 |
|---|---|---|
| `api` | DeepSeek 接口自己失败（401/402/403/429/400/5xx，或连不上它） | `api_status`（确有 HTTP 响应时） |
| `target` | 目标站点/到它的网络（dns/超时/反爬/不支持类型/404） | `target_status` 且 **≥400**；2xx/3xx/206 一律不出现 |
| `page` | 内容质量问题（上游 200 但正文是错误页） | 无 |
| `local` | 我们自己的拦截（私网地址、参数序列化失败） | 无 |

`stage` 由产生结论的那一层**显式赋值**，不靠 reason 反推 —— `blocked` 既可能来自 DeepSeek 403，也可能来自目标站 403，反推会错。`request_id` 保留，用于找官方核对。

**2. 截断提示改成模型可执行的建议**
原文案写「调大 search_max_chars」，但该参数不在工具签名里（模型调不到）。现改为「只返回前 X/Y 条；如需更多请减少 count 或关闭 need_content 后重试」，配置名不出现在模型可见文案中。

### 2026-10-25 第四轮真机回归后的修复

**1. 429 从 `blocked` 拆出来**
目标站 429 原来是和 401/403 一起归 `blocked`（「反爬/需登录」），会把「该退避重试」误导成「该换来源」。现在 `rate_limited` 单独一类（`webFailMessage` 也不再写死 429，因为目标站的 429 不带 api 状态码）。分类逻辑抽成纯函数 `classifyTargetResponse(status, contentType)`，可单测。

**2. `need_content=true` 时不再同时返回 summary**
上游会同时给 `summary`（600–800 字）和 `content`（全文），同一篇正文占两遍预算——这正是 `count=20` 只回来 17 条的直接原因。现在正文存在时只给 `content`；个别结果抽不出正文才回退给 `summary`（`summary` 字段加 `omitempty`）。

**3. 正文做页面外壳过滤（`cleanArticleContent`）**
保守的行级过滤，只作用于 fetch 的 `content` 与 search 的 `content`：
- 短行（≤40 字符）命中 APP 引导/客户端提示/运营位关键词（打开APP、下载客户端、扫码、继续阅读、即将离开、责任编辑…）
- 纯互动计数行（`评论 0`、`点赞 1.2万`）
- 推荐位之后的内容（`相关推荐`/`猜你喜欢`… 起整段丢弃）
- 弹幕/表情墙（符号占比 >80% 且不短的行）
- 连续空行压缩

安全边界：**长行一律不动**（宁留噪声不误删正文）；**不做行去重**（会毁掉 JSON 的缩进结构，单测钉住 GitHub JSON 原样返回）。

**4. 超长 query 改为报错**
原来超 128 字符静默截断，长句被当关键词匹配（实测返回完全无关的旧文）。现在直接报错「query 过长（N 字符，上限 128），请提炼关键词」。

## 能力边界（实测）

| 能力 | 边界 |
|---|---|
| 搜索 | 覆盖中文时效类信息较好；同一 query 会返回同文不同源的转载，已按 URL/低分清洗；`date` 为上架/索引时间，**不可靠**（实测 2022 年稿件标 2025-09），不要当发布时间 |
| 抓取 | 静态页、官方文档、GitHub API、微信公众号长文、B 站 SPA 可抓；**PDF/压缩包等非网页内容不支持** |
| 抓取网络 | 中文维基与英文维基上下游都超时（网络层不通，两侧都到不了）；知乎等反爬站点上游 404、本地 403 |
| 抓取失败 | 上游 404 无类型信息，靠本地探测分类（dns_error / timeout / blocked / rate_limited / unsupported_content / not_found / target_error / upstream_only），有额外耗时（≤5s）与一次本地请求 |
| 软 404 | 上游对 SPA 死链可能返回 200 + 站点外壳，靠「正文 <800 字符 + not-found 文案」识别，命中则判失败 |
| 正文质量 | 已过滤 APP 引导/互动计数/推荐位/表情墙；长行不动，仍可能残留站点特有噪声 |
| 计费 | Search / Fetch 各 5 元/1000 次成功请求；额度预扣（失败也占额度），与 token 计费无关 |
