# 附表 B · 表结构与索引（`events.db`）

上游：[活动埋点系统 · 实施技术方案](./活动埋点系统 · 实施技术方案.md) ｜ [附表 A 事件字典](./附表 A · 事件字典.md)

**两条口径**：一是这张库里只有 `meta`、`resources`、`resource_aliases` 三张主数据表，外加 `events`（事实）与 `event_refs(source='explicit')`（显式归属）**不可整体清空重算**，其余全部可重算（重算清 `event_refs` 时只删 `source<>'explicit'` 的行）；二是 `events` 表 **append-only、永不 UPDATE**，派生列一律不进事件表。

> **explicit 现状（2026-09-28 定）**：**没有任何代码写 `event_refs(source='explicit')`**。设计里 explicit 是归属第 1 层（助手发起 → `parent_event_id`、工具调用 → `call_id`、所属对话 → `conversation_id`），但这些「写入即知」的直接关系**由 `events` 表的列承载**，不再往 `event_refs` 抄一份（因果与归属分属两套语义，附表 B 顶层口径「两套语义不混一个字段」）。故 `event_refs` 实际只有 `source='resource'` 行。`重算只删 source<>'explicit'` 的条件保留为**防御**：将来若补 explicit 生产者，天然不被重算删掉。

模板：字段与索引写法照抄现有的 `llm_call_io`（审计表 + 仓储），但不与它共库；**清理写法不要照抄**（它那条 `.Limit(n)` 被 GORM 静默忽略，见下方警告框）。

## 1. 事实层

```sql
CREATE TABLE events (
  event_id      TEXT PRIMARY KEY,          -- 客户端或服务端生成，冲突即忽略
  type          TEXT NOT NULL,             -- domain.object.action
  occurred_at   TEXT NOT NULL,             -- ISO 8601 带时区，由产生方给
  received_at   TEXT NOT NULL,             -- 落库时间
  actor         TEXT NOT NULL,             -- user / assistant / system
  trigger       TEXT NOT NULL,             -- manual / batch / schedule
  outcome       TEXT,                      -- completed / failed / interrupted / cancelled
  error_type    TEXT,
  error_msg     TEXT,
  parent_event_id TEXT,                    -- 因果父；根事件必须为 NULL
  pair_id       TEXT,                      -- 区间配对，不用 parent_event_id
  launch_id        TEXT,                   -- epoch 标记，按事件类型带
  focus_epoch_id   TEXT,
  component_instance_id TEXT,
  wake_epoch_id    TEXT,
  conversation_id  TEXT,                   -- 服务端补，客户端事件由下发 payload 带
  turn_id          TEXT,
  call_id          TEXT,
  client_instance_id TEXT,
  client_build     TEXT,
  os_version       TEXT,
  attrs         TEXT,                      -- JSON，可扩展的那一格
  biz_key       TEXT,                      -- 业务键：只放单个全局唯一 id（见附表 A 的硬规则）；凑不出就 NULL
  schema_version INTEGER NOT NULL,
  CHECK (parent_event_id IS NULL OR parent_event_id <> event_id)
);
CREATE INDEX idx_events_time_type ON events(occurred_at, type);
CREATE INDEX idx_events_type_time ON events(type, occurred_at);
-- 第二道幂等防线：只对有业务键的事件生效（部分索引，NULL 不参与）
CREATE UNIQUE INDEX uq_events_bizkey ON events(type, biz_key) WHERE biz_key IS NOT NULL;
-- 有意不建索引：conversation_id、parent_event_id（量级小，先按时间取再内存过滤；将来做因果展开再补）
```

- 为什么留 `received_at`：离线补投时 `occurred_at` 与落库时间会差很远，排序用 `(occurred_at, received_at, event_id)` 三级兜底，防本地时钟回拨。
- `attrs` **不建索引**。某个 attrs 字段一旦常常拿来筛，就把它提升为真列。
- 业务键唯一约束是兜底重复上报的第二道防线，**且只对服务端可能重放的事件启用**（规则见附表 A）；第一道是 `event_id` 主键冲突忽略。`occurred_at` 精度定死到毫秒。

```sql
CREATE TABLE event_refs (           -- 归属：多挂，不设 primary
  event_id   TEXT NOT NULL,
  ref_type   TEXT NOT NULL,         -- conversation / memory / plan / reminder / subtask
  ref_id     TEXT NOT NULL,
  source     TEXT NOT NULL,         -- explicit / time_window / resource / semantic
  confidence REAL DEFAULT 1.0,
  PRIMARY KEY (event_id, ref_type, ref_id, source)
);
CREATE INDEX idx_refs_target ON event_refs(ref_type, ref_id, event_id);
```

**`event_refs` 是混合表**：`source='explicit'` 的行是事实（写入即落、永不重算），其余（time_window / resource / semantic）是派生，重算时只清这一部分。**`event_resources` 是纯派生**，DDL 见第 3 节。

`ref_type` 只有五类：会话、记忆、计划、提醒、子任务。**没有主题**（本方案不把 theme 当归属锚；它是否彻底存废由记忆系统定），**没有 note**（笔记只读、存量不迁移），记忆一律用 `memory_id`，**不许挂 topic 字符串**。

## 2. 主数据（只增量维护，不参与重算）

```sql
CREATE TABLE resources (            -- 规范锚
  resource_id   TEXT PRIMARY KEY,   -- 归一后的规范 id
  resource_kind TEXT NOT NULL,
  canonical     TEXT NOT NULL,      -- 规范形态（URL 归一后、仓库 remote 规范化后）
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE resource_aliases (     -- 见过的所有写法；"我见过两种写法"这件事不能被重算掉
  resource_kind TEXT NOT NULL,
  alias         TEXT NOT NULL,      -- 原始写法
  resource_id   TEXT NOT NULL,
  first_seen    TEXT NOT NULL,
  last_seen     TEXT NOT NULL,
  PRIMARY KEY (resource_kind, alias)
);
```

别名表要把两件事分开：**见过哪些写法**（只增，永不因重算而丢）与**指向哪个锚**（规则变更时允许重指——两个仓库锚合并后，把旧 alias 指到新锚，更新 `resource_id` 并刷新 `last_seen`）。所以它的纪律是"只增不清、可重指"，不是"写完就不动"。

## 3. 派生层（全部可清空重算；版本列按表带，见各表说明）

```sql
CREATE TABLE interval_closed (      -- 区间闭合结果
  start_event_id TEXT PRIMARY KEY,
  end_event_id   TEXT,              -- 有配对时才有
  end_at         TEXT NOT NULL,
  duration_ms    INTEGER NOT NULL,
  closed_by      TEXT NOT NULL,     -- paired / boundary / inferred / fallback / superseded
  rule_version   INTEGER NOT NULL,
  computed_at    TEXT NOT NULL
);

CREATE TABLE sessions (             -- L0–L4 五层区间
  session_id     TEXT PRIMARY KEY,
  level          INTEGER NOT NULL,  -- 0..4
  parent_id      TEXT,              -- 包含关系只在这里物化
  anchor_id      TEXT,              -- launch_id / focus_epoch_id / component_instance_id / wake_epoch_id
  started_at     TEXT NOT NULL,
  ended_at       TEXT,              -- NULL 表示尚未闭合
  closed_by      TEXT,
  threshold_version INTEGER NOT NULL,
  computed_at    TEXT NOT NULL
);
CREATE INDEX idx_sessions_level_time ON sessions(level, started_at);

CREATE TABLE event_session (        -- 映射：读取最高频，必须物化
  event_id   TEXT NOT NULL,
  level      INTEGER NOT NULL,
  session_id TEXT NOT NULL,
  PRIMARY KEY (event_id, level)
  -- 不单独带版本列：随 sessions 同批原子重建，过期判定看 sessions.threshold_version
);
CREATE INDEX idx_evsess_session ON event_session(session_id, event_id);

CREATE TABLE event_resources (      -- 这条事件碰了哪些资源；纯派生，可整表重建
  event_id      TEXT NOT NULL,
  resource_kind TEXT NOT NULL,      -- repo / url / doc / file / memory
  resource_id   TEXT NOT NULL,      -- 归一后的规范 id
  raw_value     TEXT,               -- 原始写法，冗余一份方便查询
  PRIMARY KEY (event_id, resource_kind, resource_id)
);
CREATE INDEX idx_res_event ON event_resources(resource_kind, resource_id, event_id);
-- 重建输入是 events 上留的原始值（原始 URL / 路径在采集时就落在事件上）；
-- 规则一变（比如两个仓库锚其实是同一个）就整表重跑。

CREATE TABLE day_stats (            -- 每日分布：小时 × 来源 × 类型 × actor
  day        TEXT NOT NULL,         -- YYYY-MM-DD
  hour       INTEGER NOT NULL,
  source     TEXT NOT NULL,         -- type 的第一段，不新造映射表
  type       TEXT NOT NULL,
  actor      TEXT NOT NULL,
  cnt        INTEGER NOT NULL,      -- 计数按 occurred_at 归起始小时，不摊
  duration_ms INTEGER NOT NULL,     -- 时长按重叠摊到各小时
  threshold_version INTEGER NOT NULL,
  PRIMARY KEY (day, hour, source, type, actor)
);

CREATE TABLE day_health (           -- 体检：回答"这份数据可不可信"
  day        TEXT PRIMARY KEY,
  inferred_ratio  REAL,             -- inferred + fallback 占比
  fallback_count  INTEGER,
  offline_gaps    TEXT,             -- JSON：L0 断的时段（人不在）
  collection_gaps TEXT,             -- JSON：L1 活着却零事件的时段（采集故障，必须分开报）
  clock_anomalies INTEGER,          -- 乱序、超上限被截断、重复 start
  threshold_version INTEGER NOT NULL,
  computed_at     TEXT NOT NULL
);
```

**并发度分两个数**，不合成一个：`你的并发度`（同一时刻 `actor=user` 的活跃区间数）与`系统负载`（`actor` 为 assistant/system）。前者答"你在几个地方"，后者答"我后台跑了多少"。

## 4. 边（本次不实现，表先留）

```sql
CREATE TABLE event_edges (          -- 弱关联边；设计已定、实现延后
  from_event_id TEXT NOT NULL,
  to_event_id   TEXT NOT NULL,
  relation_type TEXT NOT NULL,      -- related / same_task
  status        TEXT NOT NULL,      -- candidate / confirmed / rejected
  confidence    REAL,
  source        TEXT,               -- resource / topic / semantic / human
  threshold_version INTEGER NOT NULL,
  embedding_model_version TEXT,     -- 产生块向量时的模型版本（换模型＝换向量空间）
  created_at    TEXT NOT NULL,
  PRIMARY KEY (from_event_id, to_event_id, relation_type)
);
```

`status=rejected` 的行是重算时的**负样本**，必须参与过滤，否则同一批误报会反复出现。

## 5. 元表

```sql
CREATE TABLE meta (                 -- 版本与开关
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
-- 预置键：schema_version / rule_version / threshold_version / normalize_rule_version
--        last_recompute_at / dirty_days（JSON 数组，版本变更时打的脏标记）
--        activity.enabled（采集开关的实际生效值，默认开；键名沿用 config 路径，启动时写入，让"这台到底采没采"可查）
```

`dirty_days` 是"版本标记"的落点：规则或阈值一变就写进去，每晚定时任务先跑脏的天，再跑当天。

## 6. 清理与保留

- 明细滚动保留 **90 天**，硬删除。清理按下方的 rowid 子查询写法批量删、避免长事务——**不要照抄 `llm_call_io` 的清理写法**。
- 派生表与事实表同期清理；清掉某天事件时，同一天的派生结果一并删。`event_edges` 本次不实现但表先留，事件老化后会留下孤儿边，同样要按这个口径清掉。
- 聚合视图与事实同期，不搞两套保留期。
- **不与内容对象联动删除（2026-09-26 已定）**：删记忆或文档不连带清痕迹；要清就按 `resource_id` 走手动清的口子。
- **提醒**：90 天是硬删除、不可逆，"去年同期在干什么"届时查不到；需要长期回顾时再加按天摘要表，加之前先确认口径够用。

```sql
-- 清理示例。注意：**不要照抄主库 llm_call_io 的写法**（原因见下方警告），
-- GORM 的 .Limit() 在 DELETE 上会被静默忽略；一律用 rowid 子查询限批。
DELETE FROM events         WHERE rowid IN (SELECT rowid FROM events         WHERE occurred_at < :cutoff LIMIT :batch);
DELETE FROM event_refs     WHERE rowid IN (SELECT rowid FROM event_refs     WHERE event_id NOT IN (SELECT event_id FROM events) LIMIT :batch);
DELETE FROM event_resources WHERE rowid IN (SELECT rowid FROM event_resources WHERE event_id NOT IN (SELECT event_id FROM events) LIMIT :batch);
DELETE FROM interval_closed WHERE rowid IN (SELECT rowid FROM interval_closed WHERE start_event_id NOT IN (SELECT event_id FROM events) LIMIT :batch);
DELETE FROM sessions       WHERE rowid IN (SELECT rowid FROM sessions       WHERE started_at < :cutoff LIMIT :batch);
DELETE FROM event_session  WHERE rowid IN (SELECT rowid FROM event_session  WHERE event_id NOT IN (SELECT event_id FROM events) LIMIT :batch);
DELETE FROM event_edges     WHERE rowid IN (SELECT rowid FROM event_edges     WHERE from_event_id NOT IN (SELECT event_id FROM events) OR to_event_id NOT IN (SELECT event_id FROM events) LIMIT :batch);
DELETE FROM day_stats      WHERE day < :cutoff_day;
DELETE FROM day_health     WHERE day < :cutoff_day;
```

**⚠️ 顺手查出的主库既有 bug（不在本方案范围，但要单独开一条待办）**：`llm_call_io` 的清理用了 GORM 的 `.Limit(limit)`，而 GORM v2 构建 DELETE 的子句是 `[]string{"DELETE","FROM","WHERE"}`、不输出 LIMIT，于是这个限批被静默忽略——真到清理时会一条语句删光全部过期行（现有测试只有一条旧行，测不出来）。本方案的清理一律用上面的 rowid 子查询写法；主库那条建议一并修，但那是另一件事，不进本方案。

## 7. 部署形态与位置

- **本地与云端都采（2026-10-03 修订，覆盖原「只采本地」）**：Mac 客户端回归连阿里云后仍是单一采集器、单用户。`events.db` 自包含 SQLite、与主库无关；云端用纯 Go 驱动（glebarez/modernc）避免 CGO 依赖。
- 采集开关 `activity.enabled` 默认开；云端 `APP_ENV=prod` 现也**显式 `enabled: true`**，并须配 `db_path`（`data/events.db`）与 `day_timezone: Asia/Shanghai`（主机为 UTC，否则日期边界错 8 小时）。
- 位置：本地默认与主库同目录（`~/Library/Application Support/xhanHelper/events.db`；dev 沙盒 `xhanHelper-dev`）；云端由 `activity.db_path` 指定为 `<server>/data/events.db`。
- **纳入每日 dump（2026-09-26 已定）**：跟主库一起每天备份。注意它开的是 WAL——备份要走 SQLite 的 backup API 或先 checkpoint，裸拷文件可能拿到不一致快照。
- 与主库**不共用连接**；跨库不做 join，串联在应用层算。
