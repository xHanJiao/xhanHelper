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
- 结论：**上限得由工具自己兜**。`fetch` 正文按 `max_content_chars`（默认 8000 **字符**，按 rune 截，不会切坏中文）截；search 结果的总预算也用同一个值，超了就不再追加后续条目并标 `truncated`。注意 `max_content_chars` 要 ≤ 20000，否则历史回放还会再砍一刀（前后不一致）。

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
  search_count: 8             # 模型不传 count 时的条数；模型传的值夹在 1..20
  max_content_chars: 8000     # fetch 正文 / search need_content 的单条上限
  daily_limit: 1000           # 每日 search+fetch 合计上限；0=不限制
```

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
