# 回归云端：Mac 切回阿里云 + 本地数据迁回 + activity 上云

> 把当前「双击 app 起本地 Go + SQLite」的形态，切回「Mac app 连阿里云（MySQL）」，保留本地能力代码但不再启动本地后端；同时把本地期间新增的数据（主库 + 图片 + 活动埋点）迁回云端，并让 activity 子系统在云端也可用。

---

## 估算

- 开工粗估：`16–18 面 / 10 缝（含 2 个探针 gate）/ 5–7 次真机往返 / 决策已定` → **约 22–42h**

> 面数较上一版上调，因调研新增了「纯 Go SQLite 驱动适配」「两个探针 gate」「图片二进制资产迁移」三块。

---

## 实施进度（2026-10-03）

| 阶段 | 状态 | 落地 |
|------|------|------|
| Phase 1 · 纯 Go SQLite 驱动 | ✅ 代码完成 | `internal/activity/db.go`（glebarez + `_pragma` 语法 + MkdirAll）、`track_test.go`；`go.mod` 加 `github.com/glebarez/sqlite`。**Gate-1 通过**：`CGO_ENABLED=0 go test ./internal/activity/` 绿 |
| Phase 2 · activity 上云代码 | ✅ 代码完成 | `config.go`（`db_path`/`day_timezone` + `DayLocation()`）、`main.go:setupActivity`（去 sqlite 门槛）、`session.go:SetDayLocation`、`router.go`（`/api/health` 加 `activity_enabled`）、`conf/config.prod.yaml` |
| Phase 3 · 客户端能力探测 | ✅ 代码完成 | `ActivityClient.swift`（`ActivityCapability`）、`DeviceManager.swift`（`refreshActivityCapability` + 门控）；Mac/iOS `swiftc -typecheck` 0 error |
| Phase 4 · `sqlite2mysql` | ✅ 代码完成 + **真 MySQL 端到端通过** | `cmd/sqlite2mysql/`（**导入新库**、date 归一、concept 去重、UTF-8 清洗、零值时间→NULL、字节+行数双约束分批、跳过 `llm_call_ios`、Gate-2 探针、`-dry-run`/`-db`/`-recreate`）+ 单测。见下「端到端实测」 |
| Phase 5 · 迁移脚本 | ✅ | `scripts/export-local-data.sh`、`scripts/dump-db.sh`（补 events.db 备份）、`Makefile:build-sqlite2mysql` |
| Phase 6 · 验收 | ⏳ 待真机 | 服务端部署、导新库、切配置重启、Mac 切地址、真机跑聊天（需用户执行） |

### 端到端实测（2026-10-03，本地 MySQL 9.6，导入 `xhan_helper_test` 空库）

dev 沙盒库（731 MB，50,983 messages）→ 真 MySQL：**37 表全部对账 ✅（36 参与对账 + `llm_call_ios` 跳过），合计 92,129 行，24s**；重跑一次幂等一致；非空库不加 `-recreate` 被正确拒绝（退出码 1）。

实测中暴露并修复了 4 个**会真正阻断迁移**的问题（均为源数据/模型既有问题，非工具逻辑）：

| # | 现象 | 根因 | 修法 |
|---|------|------|------|
| A | `Error 1366 Incorrect string value: '\xE5'` | 源库 24 条 `messages.content` 在 20000/2000 字节处被按字节截断，留下非法 UTF-8；SQLite 容忍、MySQL 拒收 | 拷贝时对所有字符串字段 `strings.ToValidUTF8(_, "\uFFFD")`，计数并告警（不静默） |
| B | `Error 1292 Incorrect datetime value: '0000-00-00'` | `user_memories.last_confirmed_at` 等含 Go 零值 `time.Time`（`0001-01-01`）；GORM 结构体插入路径写零值 | 改用 `[]map[string]any` 插入（GORM `ConvertSliceOfMapToValuesForCreate`），零值时间显式写 `NULL` |
| C | `Error 1406 Data too long for column 'history_state_text'` | `theme_states.history_state_text` 声明 `type:text`（64 KB），append-only 历史已达 113 KB | 模型改 `type:longtext`（`state_text` 一并改；云端潜在同类 bug） |
| D | `Error 1390 Prepared statement contains too many placeholders` | 纯字节分批下 `llm_cost_details` 一批 5158 行 ×13+ 列超 MySQL 65535 占位符上限 | 加行数上限 `maxPlaceholders(30000)/列数`，与字节上限共同约束 |
| E | 字节分批形同虚设（推演+复核） | `estimateRowBytes` 只看 `Kind()==String`，而模型大字段几乎全是 `*string`（`Embedding.Embedding`、`Message.RequestJSON` 等），漏算 → 整表可能挤进一条 INSERT，有超 `max_allowed_packet` 风险；同一洞也让 `normalizeRow` 的 UTF-8 清洗漏掉 `*string` | 二者都解引用指针再计/清洗；补单测（含 `*string` 大字段）；运行时打印 `SHOW VARIABLES LIKE 'max_allowed_packet'` |

> A/B 属数据修正，工具会打日志说明；C 是模型层修复（需连同代码一起提交）；D/E 是分批与估算修复，E 同时是 UTF-8 清洗的完整性问题。修复后重跑 e2e：36/36 ✅、92,129 行、20s。

**实际改动文件清单**：`server/internal/activity/{db.go,session.go}`、`server/internal/activity/track_test.go`、`server/internal/config/config.go`、`server/cmd/xhan-helper/main.go`、`server/internal/controller/router.go`、`server/internal/model/theme_state.go`、`server/conf/config.prod.yaml`、`server/cmd/sqlite2mysql/{main.go,main_test.go}`、`server/scripts/{export-local-data.sh,dump-db.sh}`、`server/Makefile`、`client/xHelper/xHelper/Shared/Services/{ActivityClient.swift,DeviceManager.swift}`、`server/go.{mod,sum}`。

**执行命令**：
```bash
# 1. 本地导出（Mac，先退出 app）
bash server/scripts/export-local-data.sh
# 2. 编译上传迁移工具（Mac）
make -C server build-sqlite2mysql
# 3. 管理员在新库上授权（工具不建库）
#    CREATE DATABASE xhan_helper_cloud CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
#    GRANT ALL PRIVILEGES ON xhan_helper_cloud.* TO 'xhan_helper_user'@'localhost';
# 4. 服务器：预演 → 导入新库（旧库不动，非空库会被拒绝）
APP_ENV=prod ./sqlite2mysql -source ./upload/xhan_helper.snapshot -dry-run
APP_ENV=prod ./sqlite2mysql -source ./upload/xhan_helper.snapshot -to prod -db xhan_helper_cloud -confirm=yes
# 3b. 只有重灌新库时才加 -recreate；它必须与 -db 同用，目标名==默认库会被拒绝
#     APP_ENV=prod ./sqlite2mysql -source ... -to prod -db xhan_helper_cloud -recreate -confirm=yes
# 4. 图片与 events.db 落位（Phase 5）——**必须仍在停服窗口内**：
#    服务一旦以 activity=enabled 启动就会持有 events.db 句柄，运行中覆盖会 inode 分叉。
tar -xzf upload/images.tar.gz -C data/ ; cp upload/events.db data/events.db
# 5. 先改 config.prod.yaml 的 db.database=xhan_helper_cloud，再 make deploy（顺序要紧：
#    新二进制连旧库时会触发计划外 AutoMigrate DDL）
# 6. 验：/api/health.activity_enabled=true、数据齐全 → 再切客户端地址。回退＝改回旧库名重启。
```

---

## 零、一句话目标

1. **Mac app 改一个设置就连回阿里云**：设置 →「服务器 → 服务地址」填 `https://helper.xhanx.cn`，重启后用云账号登录，之后随时可连。**本地部署能力与数据保留，只是不再启动本地后端。**
2. **本地这几天新增的数据搬到云端**：主库导入全新库、图片文件补齐、活动埋点续上。
3. **activity 在云端可用**：cloud 服务端启用 events.db（留存服务器文件），客户端经能力探测自动开启采集。

---

## 一、现状与关键事实

### 1.1 逃生口已经存在，切回云端不用改代码

- 设置页「服务器 → 服务地址」=`App.swift:818`（key `xhan_server_url`，按环境加后缀）。
- `LocalBackendManager.isActive`（`LocalBackendManager.swift:52`）判空：留空=连内嵌本地后端；填了=连该地址且**不启动本地后端**。
- `localBackendRoot`（`App.swift:662`）：`!isActive` 时直接走 `accountRoot → LoginMacView`（原来的云端登录流程）。
- iOS 客户端始终连云端（`DeviceManager.baseURL` 的 `#else` 分支），不受影响。

### 1.2 云端后端在线，但代码是旧的

- `https://helper.xhanx.cn/api/auth/login` 正常返回 400（服务活着）。
- `/api/health` 返回 404，而该路由 `2026-09-24`（commit `b4e605d9`）才加入 → 云端跑的是 9/24 之前的二进制。
- 本地使用期间云端未产生新数据（用户确认），因此本地库是「云端快照 + 后续新增」的**超集**。据此**导入一个全新库**（旧库原样留作回退），而非原地覆盖（2026-10-03 修订）。

### 1.3 本地数据规模

本地 prod 库 `~/Library/Application Support/xhanHelper/xhan_helper.db`：

| 项 | 值 |
|----|----|
| 主库大小 | 919 MB |
| conversations / messages | 885 / 53,536 |
| user_memories / notes / documents / bills | 936 / 802 / 236 / 992 |
| embeddings | 10,325 |
| 消息时间范围 | 至 2026-10-02 |
| events.db | 25 MB + 11 MB WAL，20,155 事件（9/25 起）|
| images | 41 文件 / 20 MB（image_assets 57 行，16 行文件缺失）|

---

## 二、决策记录（已锁定）

| # | 决策 | 理由 |
|---|------|------|
| D1 | Mac 用逃生口切回云端；保留本地能力不删 | 改动最小、可逆；本地数据形态留档 |
| D2 | 主库导入一个**全新的 MySQL 库**，再切 `config.prod.yaml` 的 `db.database` 指过去（**不覆盖旧库**） | 旧库原样保留即回退点；导入失败只丢新库、重跑即可，无 P0 破坏风险（2026-10-03 由「整库覆盖」修订） |
| D3 | 迁移范围：除 `llm_call_ios` 外全搬 | 该表 7 天保留，属纯调试日志 |
| D4 | `concept_embeddings` 大小写重复保留 `created_at` 较新者 | 云端 CI 排序，重复会违反唯一索引 |
| D5 | SQLite 驱动改**纯 Go**（`github.com/glebarez/sqlite`，底层 modernc） | 云端 Linux 交叉编译 CGO off，mattn 无法开 sqlite |
| D6 | `sqlite2mysql` 上传到服务器本地跑，加 `-db <新库名>` 与「非空即拒绝」闸门 | 纯 Go 驱动可 CGO off 跨编译；防手滑指向有数据的旧库 |
| D7 | activity 上云：events.db 留服务器文件；历史直接拷；客户端能力探测自动开 | 子系统自包含 sqlite，不碰主库 |
| D8 | activity `day_timezone=Asia/Shanghai`（新增配置项） | `sessionDayLoc=time.Local` 无 setter，云端 UTC 会让「一天」错 8 小时 |
| D9 | 图片文件随主库一起迁 | 否则历史聊天图 404 |

---

## 三、关键调研结论

### 3.1 主库迁移的三个真实阻塞点

1. **`date` 列带 `Z`（严格模式 Error 1292）**
   - `LlmCostDetail.Date`、`LlmModelPrice.EffectiveDate` 是 `string` 映射 `type:date`。
   - 本地 SQLite：`llm_cost_details.date` 11,526 行是 `2026-08-12T00:00:00Z`、4,503 行是 `2026-10-01`；`llm_model_prices.effective_date` 9 行**全带 `Z`**。
   - MySQL `DATE` 不接受尾部 `Z`。**拷贝前/拷贝时截成 `YYYY-MM-DD`。**

2. **`concept_embeddings` 大小写唯一冲突（Error 1062）**
   - 云端库/表排序规则为 `utf8mb4_0900_ai_ci`（大小写不敏感），本地 SQLite 为 BINARY。
   - 实测存在 `type=topic, value="Agent开发"` 与 `"agent开发"`（同 user）两条 → 插入第二条触发唯一索引冲突。
   - **按 `(type, lower(value), user_id)` 检测，保留 `created_at` 较新者并记录跳过条目。**

3. **批量 Packet 超限**
   - `messages` 平均 ~3.3 KB，但 top-20 行合计 ~10.7 MB、50 行可达 ~27 MB。
   - 照搬 `mysql2sqlite` 的 `batchSize=50`，在 `max_allowed_packet` 非 64 MB 时会 `Packet too large`。
   - **按字节自适应分批（~1–2 MB/批），运行时打印 `SHOW VARIABLES LIKE 'max_allowed_packet'`。**

### 3.2 已排查、确认无雷

- 无 index-on-TEXT、无 TEXT default；9 个 `type:json` 列 0 非法/0 空串；无零值日期；`conversations.extra` 无 NULL。
- 所有 datetime 列均带偏移（绝对时刻不丢）。
- 全库仅一条外键 `messages → conversations`。
- 所有 `size:N` 列实测 max 未超（`embedding_chunks.parent_id` 36=36、`concept_embeddings.id` 19/20 为仅有的贴边）。
- 云端 dump 中**无触发器/存储过程**（`50003 TRIGGER` 计数 0）。
- 旧云端的孤儿表（`facts`/`fact_changelogs`/`missions`/`mailbox_items`/`pending_findings`/`commitments`）不在新库出现（新库只建 AllModels）；导入新库不需要清理它们。

### 3.3 Activity 上云的两个静默坑

1. **时区**：`sessionDayLoc = time.Local`（`activity/session.go:29`），**无生产 setter**；云端主机 UTC 会让 `day_stats`/`sessions`/`DaySkeleton` 的「一天」边界错 8 小时，且读/派生共用同一个错变量，**无任何告警**。项目已有 `config.BJ`（Asia/Shanghai），需接上。
2. **路径**：`activity.DBPath(cfg.DB.Database)` 在 MySQL 下把库名当路径，得到 `./events.db`。需新增 `activity.db_path`；主库非 sqlite 且未配置时应**启动 fatal**，禁止静默写当前目录。

### 3.4 隐藏 blocker：云端 Linux 二进制开不了 SQLite（CGO）

- `make build-linux`（`Makefile:327`）为 `GOOS=linux GOARCH=amd64 go build`，**未显式 `CGO_ENABLED=1`**；交叉编译时 Go 默认关闭 cgo。
- `mattn/go-sqlite3` 在 `!cgo` 下走 `static_mock.go`，`SQLiteDriver.Open` 直接返回 `"go-sqlite3 requires cgo to work"`。
- 本机无 zig / musl-gcc / linux-gcc / docker；`go env CC=cc` 无法交叉编译 cgo。
- 后果：
  1. activity 若在云端启用会**静默失败**（`setupActivity` 按可降级记日志返回 nil）。
  2. `sqlite2mysql` 放服务器也读不了 SQLite。
- **对策 D5/D6**：activity 与迁移工具的 SQLite 开库统一换纯 Go 驱动；主库仍用 mattn（Mac 原生 cgo）。

### 3.5 图片二进制资产

- `images/` 41 文件 / 20 MB；`image_assets` 57 行 → 16 行文件已缺失（服务端有 file-missing 自愈会清）。
- 全部 `visibility=private, source=chat`，被 23 条消息引用。不拷贝则 Mac 历史聊天图 404。

---

## 四、分阶段方案

### Phase 0 · 快照与体检

- 退出 Mac app（WAL 落盘）。
- 主库出一致快照：`sqlite3 xhan_helper.db ".backup <snapshot>"`。
- `events.db`：`PRAGMA wal_checkpoint(TRUNCATE)`（当前有 11 MB WAL 未 checkpoint）。
- 云端 `make db-pull` / `dump-db.sh` 备份 MySQL；`df -h`；`SHOW VARIABLES LIKE 'max_allowed_packet'`。

### Phase 1 · 纯 Go SQLite 驱动（先做，决定后续可行性）

| # | 改动 | 文件 |
|---|------|------|
| 1 | 加依赖 `github.com/glebarez/sqlite`（底层 `modernc.org/sqlite`） | `go.mod` |
| 2 | activity 开库换纯 Go 驱动；DSN 改 `?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)` | `internal/activity/db.go` |
| 3 | 测试 import 同步（`track_test.go` 等） | `internal/activity/*_test.go` |

- **主库 `service.NewDB`（mattn）不动**——Mac 原生 cgo 正常，避免影响本地形态。
- **探针门槛（Gate-1）**：最小测试建 events.db → schema → `ON CONFLICT` → 部分唯一索引（`WHERE biz_key IS NOT NULL`）→ ingest → `Recompute`/`Cleanup`，纯 Go 驱动全绿才继续。
- 跑 `go build ./... && go test ./...`。

### Phase 2 · activity 上云代码

| # | 改动 | 文件 |
|---|------|------|
| 1 | 新增 `activity.db_path`（空=旧 `DBPath`；主库非 sqlite 且空→fatal）、`activity.day_timezone`（空=不变） | `internal/config/config.go` |
| 2 | 去 `driver != sqlite` 门槛；按 `db_path` 打开；`day_timezone` 非空时 `SetDayLocation`（用 `config.BJ`） | `cmd/xhan-helper/main.go`、`internal/activity/session.go`（加 setter） |
| 3 | `/api/health` 增 `activity_enabled` | `internal/controller/router.go:89` |
| 4 | prod：`enabled:true`、`watermark_mode:segment`、`db_path:data/events.db`、`day_timezone:Asia/Shanghai` | `conf/config.prod.yaml` |
| 5 | 订正「只采本地实例」口径 | `docs/design/activity-events/*` |

- 派生调度无需改：`CronRunner` 在 prod 本就运行，store 非 nil 后启动即跑一次 + 每 24h 重算+清理（`cron_runner.go:39,416-435,500-515`）。
- 客户端上传路径云端就绪：Bearer 鉴权 + **每段上传即删 = 天然 segment 语义**，无需改上传逻辑。

### Phase 3 · 客户端（macOS，需 Xcode 重编）

- 新增 `ActivityCapability` 缓存；登录 / `verifyToken` 前 `await` `/api/health` 写入。
- `ActivityClient.isEnabled = LocalBackendManager.isActive || ActivityCapability.remoteEnabled`（`ActivityClient.swift:11`）。
- `DeviceManager.activityCollectionActive`（`:115`）同步；注释订正「云端不启动」。
- iOS 不采集（monitors 为 macOS-only），天然单采集器。
- **顺序依赖：先部署服务端，再重编客户端。**

### Phase 4 · 主库迁移（导入新库）

1. 管理员建新库并授权（工具不建库）。
2. `cmd/sqlite2mysql`（蓝本 `cmd/mysql2sqlite` 反向）导入新库，**旧库不动**：
   - **目标非空即拒绝**；重灌用 `-recreate`，但**必须同时给显式 `-db <新库名>`**（目标名 == 配置默认库即 `fatal`），回退点不可被误清。
   - 源读取**用纯 Go 驱动、`file:` URI `mode=ro` 只读**（跨编译 CGO off）。
   - `SkipHooks`，`[]map[string]any` 插入（零值时间写 `NULL`）。
   - **date 归一**：`llm_cost_details.date` / `llm_model_prices.effective_date` 含 `T`/`Z` 时截 10 位并记数。
   - **concept 去重**：按 `(type, lower(value), user_id)` 保留较新者，记录跳过。
   - **UTF-8 清洗**：非法字节替换为 `U+FFFD`，计数并告警。
   - **跳过 `llm_call_ios`**；模型清单外孤儿表跳过并打印。
   - **字节 + 行数双约束分批**（≤1 MiB 且 ≤30000/列数 行）。
   - 逐表对账（计划写入 vs 目标实际）。
   - **探针（Gate-2）**：抽 `messages.created_at` 比对「驱动解析」与「原文自解析」的绝对时刻，不一致则中止、改 Mac 上跑。
3. 压缩上传快照到服务器本地跑。
4. **先改 `config.prod.yaml` 的 `db.database` 指向新库，再部署/重启**（顺序要紧：新二进制仍连旧库时，启动 AutoMigrate 会对旧库做计划外 DDL——如本次的 `theme_states` `text→longtext`。非破坏、可逆，但没必要）。**回退 = 改回旧库名重启。**
   - 注意：仓库里的 `config.prod.yaml` 默认仍是旧库名，部署前必须改好并随部署上传（`make deploy` 会上传该文件）。
   - 本步（切库）必须早于 Phase 5 的「客户端服务地址切到云端」，否则 Mac 短时连的是旧库。

### Phase 5 · 其它资产 + 切 app

- 拷贝 `images/`（41 文件）→ 云端 `storage.image_dir`（`data/images`）。
- checkpoint 后本地 `events.db` → 服务器 `$P/data/events.db`。
- `dump-db.sh` 增 events.db（WAL 安全：`sqlite3 .backup`，或停服窗口连带 `-wal/-shm` 打包）。
- 设置服务地址 = `https://helper.xhanx.cn` → 重启 → 云登录（用户名 `xhan`；`allowed_users` 只管注册、不拦登录）。

### Phase 6 · 验收

- `/api/health.activity_enabled=true`；启动日志 `[activity] events.db=… enabled=true`。
- Mac 产生浏览/输入事件 → 30s 内入库；`user_action_log.day_skeleton` 日期边界按 Asia/Shanghai 正确。
- 9/25 起 activity 历史在云端可见。
- 主库最近数据、图片、文档/会话都在；抽样 `created_at` 绝对时刻一致；iOS 能连。

---

## 五、`sqlite2mysql` 工具设计要点

- **护栏**：默认要求目标库无本工具模型表（非空即 `fatal`），防手滑指向有数据的旧库。`-db <新库名>` 指定目标；`-confirm=yes` 二次确认。**`-recreate` 必须与显式 `-db <新库名>` 同时出现**，且目标名不得等于配置里的默认库（旧库）——否则 `fatal`；这样 `-recreate` 只能作用于明确命名的新库，回退点不可被误清。旧库不参与，天然是回退点，故**无需 mysqldump**。
- **建表**：AutoMigrate 到空库；错误必须 fatal。旧的孤儿表只在 `-recreate` 时清。
- **两张 `uint` 自增表**（`llm_cost_details`、`llm_model_prices`）：map 里显式写源 ID 到自增列，MySQL 自动推进 AUTO_INCREMENT。
- **外键**：`-recreate` 时 `SET FOREIGN_KEY_CHECKS=0` 并 `pinSingleConn`（会话级设置须单连接）；首次导入按 `AllModels` 顺序即可。
- **孤儿表**：`facts`/`fact_changelogs`/`sqlite_sequence` 落在「不在 AllModels」分支，跳过并告警。

---

## 六、activity 上云设计

### 6.1 存储

- events.db 是自包含 SQLite，主库为 MySQL 无影响。
- 云端路径 `data/events.db`（与 `storage.image_dir=data/images` 同级，deploy 不覆盖）。
- 历史 events.db（同格式）直接拷，`schema_version=1` 一致。

### 6.2 能力探测

- `/api/health` 返回 `activity_enabled`（公开接口）。
- 客户端缓存为 `ActivityCapability`；`ActivityClient.isEnabled` 据此启停。本地模式短路为开。

### 6.3 时区

- 新增 `activity.day_timezone`；prod=`Asia/Shanghai`，本地留空=不变。
- 需在 `activity` 包加 exported setter，写入 `sessionDayLoc`。

### 6.4 频率（现状数据）

- 客户端每 30s `uploadNow()`：空段不封；非空封段后**一段一 POST** `/api/events/batch`。
- 实测活跃日请求 ≈ 300–800 次/天；空闲 0；理论天花板 2,880 次/天；平均 QPS ≈ 0.003–0.01，峰值每 30s 1 次（串行）。
- 服务端事件（llm/tool/chat/memory/doc/bill）**不经 HTTP**，进程内直写。

### 6.5 埋点安全

- `Emit` 非阻塞、后台 flush、写失败只记日志丢弃；GORM 回调全程 `recover()`。唯一同步点：`onBeforeUpdate` 对文档更新做一次旧文 SELECT（加延迟、不报错）。
- 队列满丢最旧（计数在 `drop`）。

---

## 七、风险与兜底

| 风险 | 兜底 |
|------|------|
| 纯 Go 驱动行为差异（WAL/部分索引/ON CONFLICT） | Gate-1 探针；不通回退 MySQL 方案 |
| 纯 Go 读主库时间格式差异 | Gate-2 探针；不通回退「Mac + SSH 隧道」迁移 |
| `date` 带 `Z` / concept 重复 / packet 超限 | Phase 4 内置对策；先 dry-run 对账再真跑 |
| 停服窗口 | 个人单用户可接受；token 全清需重登 |
| 云端 AutoMigrate 静默失败 | 工具内 fatal + 部署日志双保险 |
| 磁盘空间不足 | Phase 0 `df -h` 检查 |
| 事件埋点失败 | 只丢弃不影响业务（已验证） |

---

## 八、附录：不兼容 Web 前端的功能

切云端后 Mac 客户端能力基本保留；以下为 **Vue Web 版（helper.xhanx.cn）缺失、仅 macOS 客户端有**的能力，属既有产品差异，本次不处理：

| 功能 | 服务端 | Vue Web | macOS |
|---|---|---|---|
| 文档划线/批注 + AI 回复/连线 | ✅ | ❌ | ✅ |
| 文档 audience 三态筛选 | ✅ | ❌ 只显示 `finished`，`internal` 文档在 Web 隐形 | ✅ |
| 文档回收站（软删恢复） | ✅ | ❌ | ✅ |
| 文档多选批量删/下载 | 🟡 | ❌ | ✅ |
| 图片上传/图库/聊天传图 | ✅ | ❌ | ✅ |
| Mac 发出的聊天图在 Web 不渲染 | ✅ | ❌ | ✅ |
| 记忆 👍/👎、来源标签 | ✅ | ❌ | ✅ |
| 计划模式三态 | ✅(强制) | ❌ 无开关，可能「卡住」 | ✅ |
| 信任模式三态 | 客户端态 | ❌ | ✅ |
| 本地产物管线/内链/文档卡片 | 🟡 | ❌ | ✅ |
| shell/文件系统/浏览器/MCP 客户端工具 | 协议在 | ❌ | ✅ |
| 子 agent 实时面板 | ✅ | ❌ `subagent` SSE 被丢弃 | ✅ |
| reminders 变更 | ✅ | ❌ `reminders_changed` 被丢弃 | ✅ |
| 无痕会话 / 归档恢复 / 用量环 / llm-config | ✅ | ❌ | ✅ |
| activity 用户行为日志 | ✅（本次启用） | ❌ 无 UI | ✅ |
| Touch ID / 本地备份 / 系统通知 / 长回复提示音 / 两栏布局 / 透明卡片 / PiP / 收藏栏 / 快捷键 | — | ❌ | ✅ |

SSE 中 `subagent` / `client_tool_call` / `reminders_changed` 及裸 `thought` / `plan_done` **仅 Swift 消费**，Web 静默忽略（与 `AGENTS.md` 一致）。

---

## 九、调研证据索引（关键文件）

- 逃生口：`client/xHelper/xHelperMac/App.swift:662,818,1020`、`LocalBackendManager.swift:52`
- activity 门控：`cmd/xhan-helper/main.go:129-147`、`internal/activity/db.go:31-58`、`internal/activity/session.go:29`
- activity 客户端：`ActivityClient.swift:11`、`DeviceManager.swift:115,143`、`ActivityRecorder.swift:34,95`、`ActivityStore.swift:77`
- 派生调度：`internal/agent/cron_runner.go:39,209-223,416-435,500-515`
- 迁移蓝本：`cmd/mysql2sqlite/main.go`
- 模型：`internal/model/{llm_cost,llm_price,document,user_memory,image_asset}.go`
- CGO：`server/Makefile:327`、`go.mod`（mattn v1.14.22）
- 云端 schema：`server/data/mysqldumps/xhan_helper-20260924-224816.sql.gz`（collation `utf8mb4_0900_ai_ci`）
- 设计文档：`docs/design/local-sqlite/design.md`、`docs/design/activity-events/*`
