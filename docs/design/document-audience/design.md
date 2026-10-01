# 文档分层（audience）· 设计（人话版）

估算（开工）：14 面 / 3 缝（1 条需先建探针：MySQL 补列 + 旧行默认值）/ 1–2 次真机往返 / 0 个待拍决定 · ~4–7h

来源：`文档分层机制 · v0.1`（已定）。本文只讲「怎么落到 xhanHelper」，机制本身的口径以原文档为准。

## 一句话

给每篇文档加一个字段 `audience`，回答一个问题：**这篇是给用户看的，还是给助手后续工作用的？**
列表默认只给用户看成品；助手和工具照旧能拿到全部。

## 背景：为什么要分层

文档库 229 篇，命中「底稿」的 88 篇，标题明写底稿的三十来篇；人话版/可读版加起来不足十篇。也就是说，人真正读的成品被淹没在工作草稿里。

根因不是底稿写多了，而是**写的时候没定"给谁读"**。那批调研底稿的目标读者是当时的助手（供对表、抽结论），本质是工作内存，却被写进了永久文档库——放错了容器。

所以解法不是"少写底稿"，而是**每篇落笔时就声明读者是谁**。

## 三层容器

| 层 | 判据 | 归宿 | 例子 |
| --- | --- | --- | --- |
| 成品 | 给用户读 | 文档表，`audience=finished` | 调研人话版、日报、方案稿、SOP、台账 |
| 材料 | 给助手后续工作用 | 文档表，`audience=internal` | 调研底稿、交接、采集清单、证据池 |
| 程序数据 | 只给代码和工具读 | 不进文档表，落 Application Support | `events.db`、`llm_call_io`、客户端 `history.json` 等 |

判据只有一条，问读者是谁：用户 / 助手 / 代码。不设二级判断——读者是代码这一支，本身就已经回答了"要不要进文档系统"。

两条定性：

- 材料有终点：它是"给助手后续工作用"的，工作做完（成品出稿）就该封存降级。
- **`internal` 是"默认不呈现"，不是"不可访问"**：列表里默认不发，拿到链接照常能读。

## 关键口径（容易踩的坑）

1. **字段定义成"读者是谁"，不是"要不要显示"。** 过滤规则是消费方，不是定义。绝不能按可见性命名（hidden/visible），否则会被后来的人当权限字段用，加第三个值（仅自己）就乱套。
2. **运行时必填、无默认**，由写文档的模型显式声明，代码校验取值合法。判据一句话：这是给用户看的，还是给助手后续工作用的？
3. **DB 层兜底默认选 `internal`。** 万一有代码路径漏传，材料误上列表是旧病复发；成品被沉底只是暂时看不见、还能捞回来。代价不对称，所以兜底往"藏"的方向倒。
4. **创建时定；`update_document` 允许改。** 改动不设额外规矩——文档变更本来就落进埋点事件流，`audience` 的变更跟着走即可，留着将来排错。
5. **过滤是消费方的事。** 同一个字段，不同场景规则不同（见下表），不要把它写死成"永远过滤"。

| 场景 | 规则 |
| --- | --- |
| 用户列表下发 | 默认只 `finished`，可用列表开关切到"全部" |
| 成品正文引用 `internal` | 照常渲染、可点开读，不做过滤 |
| `internal` 正文引用 `finished` | 保留（底稿标"正式阅读稿见…"是有用的） |
| 工具检索 / 读取 | 全部可见，不受用户侧开关影响 |
| 执行位置 | 服务端过滤，客户端只拿过滤后的结果 |

## 已拍决定

- **UI 范围：只做 macOS 客户端。** Vue 前端已放弃，不再改；接口默认值顺带让 Web 也只看到成品。
- **字段名 `audience`，枚举 `finished / internal`**，不是 `hidden / visible`。
- **DB 兜底默认 `internal`**，运行时由工具强制声明。
- **回标用一次性命令 `cmd/backfill-audience`**（复用 config/DB 初始化，MySQL/SQLite 通吃），不写裸 SQL 手工跑。
- **工具的 list/query 输出把 `audience` 放进可选字段白名单，但不进默认返回**（默认输出不膨胀，需要时 `fields=audience`）。
- **internal 文档仍可公开分享**（"默认不呈现 ≠ 不可访问"），分享接口不加拦截。
- **回收站列表不做 audience 过滤**（回收站本就是杂物区）。

## 怎么落

### 服务端

1. `model/document.go`：加字段 `Audience string`（`size:20;not null;default:'internal';index`）+ 常量 `DocumentAudienceFinished / Internal` + 校验函数（仿 `conversation.go` 的 const 风格）。AutoMigrate 自动补列，旧行填 `internal`。
2. `service/document_repo.go`：`ListByAllTags` / `List` 增参 `audience string`——`""` = 全部、`finished|internal` = 过滤。
   - 工具调 `List` 传 `""`（检索/读取全部可见）。
   - controller 传过滤值。
3. `controller/document.go`：列表新增 query `audience`。缺省 = `finished`；`all` = 不过滤；`finished|internal` = 过滤；其余非法值 400。响应加 `total_all`（同 tag/keyword 条件下、不叠加 audience 过滤的总数），供开关显示数量。
   - 副作用（好的）：Web 不发参数 → 自动只拿成品，无需动 Web。
4. `tool/document.go`：
   - `create_document` 加 `audience`（required），参数描述写死判据「给用户看=finished / 给助手后续用=internal」；handler 校验空值/非法值报错。
   - `update_document` 加可选 `audience`，非法值报错。
   - `docListFields` 加 `"audience"`（不进 `docDefaultFields`）；list/query 的 `full` map 补该键。
5. `activity/track.go`：`hasAny(vals, "title", "tags")` 加 `"audience"`，使「仅改层级」也发 `doc.updated`（size_delta 0）。
6. `cmd/backfill-audience/main.go`：按 tags 机械回标 + 打印疑难清单（见下节）。

### macOS 客户端

7. `APIClient.swift`：
   - `Document` 加 `let audience: String?`（用可选，避免客户端先于服务端更新时整列表解码失败）。
   - `DocumentListResponse` 加 `totalAll: Int?`（映射 `total_all`）。
   - `listDocuments(...)` 加 `audience: String?` 参数，非空则拼 `&audience=`；`documentCount` 同步透传。
8. `DocumentViewModel.swift`：加 `@Published var audienceView`（枚举 `仅成品` / `全部`，**不持久化**——下次进面板回默认，否则默认形同虚设）；`load()/loadMore()/documentCount()` 传参；切档时 `offset=0` 重载。
9. `BrowsePanelView.swift`：在既有「列表 / 词云 / 回收站」Picker 旁加「仅成品 · N / 全部 · M」分段控件（列表模式；数量取 `total` / `totalAll`）。入口做收敛，别用红点或角标——天天提示"这里藏着东西"等于把噪音搬回来。
10. `DocumentListView.swift`：卡片 tags 里当 `audience == "internal"` 追加灰色「内部」标记，否则两堆东西分不出来。

## 存量回标

tags 现成可机械识别，**兜底可见**：

- 判 `internal`：`陪伴AI调研底稿`、`记忆层调研底稿`、`底稿`。
- 其余一律 `finished`（含 `可读版`、`人话版`，也含没打标签的方案稿/阶段汇总/交接/SOP）。
- 命令打印被判为 `internal` 的清单（标题 + tags）供你复核——这是**核对项，不是拦截项**。

方向说明：存量 229 篇里绝大多数是人真正要读的，兜底往"藏"倒会把成品一起沉底，代价比多露几篇底稿大。新写入的兜底仍走 DB 默认 `internal`（见"关键口径"第 3 条）——两处方向不同、不矛盾：**历史数据要"捞回来"，未来漏传要"别污染"。**

`cmd/backfill-audience` 幂等：重复跑结果一致；只改 audience，不碰正文。dry-run 打印**两张清单**——「将置 internal」与「将置 finished」；后者是重跑误伤的可见面（把后来按 internal 写入、tags 不含底稿词的材料放开）。`--write` 前再打一条一次性迁移警告。

## 发布时序（关键，避免列表短暂变空）

AutoMigrate 补列时，存量行会被填成列默认值 `internal`；而列表默认 `audience=finished`。若先上新二进制、再回标，中间那段窗口用户列表会命中 0 篇，像文档全丢了。

**正序**：`backfill-audience --write` → **再**启动/滚动新二进制。
`backfill-audience` 自己走 `app.InitDB`，**会先跑 AutoMigrate**（建列 + 回标一步到位），所以新服务起来时值已就位、窗口为零。

- 注意：**dry-run 也会建列**（InitDB 副作用）——prod 上先备份。
- `--write` 命中 prod 库需 `CONFIRM_PROD=yes`（`GuardProdWrite`）。
- 应急兜底：`parseDocAudience` 支持 `audience=all`，必要时可临时以"全部"为默认灰度（本项目不走这条，仅备查）。

## 验证

- 单测：repo audience 过滤；词云 `TagGraph` audience 过滤；`create_document` 缺/非法 audience 拒绝；工具 list/query 不受过滤影响（仍见 internal）；仅改 audience 发 `doc.updated`。
- `go build ./... && go test ./...` 全绿。
- Swift 源集 `swiftc -typecheck` 自检（`@main` 盲区已知，最终由用户在 Xcode 编译）。
- 真机 1 次（用户在 Xcode）：建一篇 internal → 默认列表不出现 → 切「全部」可见且带「内部」标记 → 重进面板回默认 → 工具能检索到。

## 缝

1. dev MySQL AutoMigrate 补列后旧行确为 `internal`（需真库跑一次确认）。
2. 存量回标在 dev 库跑一遍，核对两张清单（internal 应只含底稿类；finished 即"要放开谁"），确认后连同 `--write` 与发布顺序走一遍。
3. Swift 列表开关手感 + 「内部」标记观感（真机验收）。
