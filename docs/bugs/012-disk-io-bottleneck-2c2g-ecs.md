# 012 — 阿里云 2C2G ECS 磁盘 IO 读写受限问题排查与修复

> **日期**：2026-08-08  
> **环境**：阿里云 ECS，2C2G，40G 系统盘，Ubuntu  
> **栈**：nginx + Go 单体后端 (xhan-helper) + MySQL 8 (同一主机) + Python sidecar  
> **报警**：该实例在 2026年8月8日 15:40:00 出现磁盘 IO 读写受限，可能影响业务正常运行

---

## 一、现象

- 阿里云控制台报「磁盘 IO 读写受限」，提示云盘性能不足
- 服务出现卡顿（"卡死"），空闲时恢复
- 用户操作（发消息对话）时 `iostat %idle` 从 90%+ 掉到 40%
- 最近几天频繁出现，呈趋势性恶化

## 二、环境信息

| 指标 | 值 | 来源 |
|------|-----|------|
| 内存 | 1690MB total，available 1149MB，swap 0 | `free -m` |
| 磁盘 | 40G，已用 27G（72%） | `df -h /` |
| MySQL 版本 | 8.x (Ubuntu apt 安装) | `mysql --version` |
| MySQL datadir | 4.9G | `du -sh /var/lib/mysql` |
| 进程架构 | nginx + Go server(:8088) + MySQL + Python sidecar(:9090) | `ps aux` |

## 三、取证过程

### 3.1 磁盘 IO 实时观测

```bash
sudo iostat -x -m 1 10
```

**观测**：发消息时 `%idle` 从 90%+ 骤降到 40%，磁盘活跃度显著上升。与"空闲不卡、使用才卡"的症状一致——写入路径是瓶颈。

### 3.2 MySQL 参数检查

```bash
mysql -e "SHOW VARIABLES LIKE 'sync_binlog'; SHOW VARIABLES LIKE 'innodb_flush_log_at_trx_commit';
          SHOW VARIABLES LIKE 'innodb_buffer_pool_size'; SHOW VARIABLES LIKE 'binlog_format';"
```

| 参数 | 现值 | 影响 |
|------|------|------|
| `sync_binlog` | **1** | 每次事务 commit 强制 fsync 刷 binlog |
| `innodb_flush_log_at_trx_commit` | **1** | 每次事务 commit 强制 fsync 刷 redo log |
| `binlog_format` | **ROW** | 行级 binlog，大字段 UPDATE/DELETE 写完整前后镜像 |
| `innodb_buffer_pool_size` | **128M**（默认） | 只给 MySQL 128MB 缓存，2G 机器上读频繁走磁盘 |

**诊断**：三个参数叠加，每条写操作要经历 **binlog fsync + redo fsync + binlog ROW 完整镜像** 三重写放大。对于携带大字段（几十 KB/行）的业务，一次对话会上百次事务提交 = 上百次 fsync → 云盘 IOPS 被吃穿。

### 3.3 表体积统计

```bash
mysql xhan_helper -e "SELECT table_name, table_rows, ROUND(data_length/1024/1024) data_mb,
                       ROUND(index_length/1024/1024) idx_mb
                       FROM information_schema.tables WHERE table_schema='xhan_helper'
                       ORDER BY data_length DESC LIMIT 15;"
```

| 表 | 行数 | data_mb | 备注 |
|----|------|---------|------|
| messages | 11900 | **1521** | ⚠️ 平均 128KB/行 |
| embeddings | 12380 | 394 | 向量数据，正常 |
| reflection_llm_calls | 2046 | **145** | ⚠️ 平均 74KB/行 |
| reflection_decisions | 2482 | 29 | |
| user_memories | 389 | 14 | |

### 3.4 大字段构成分析

```bash
mysql xhan_helper -e "SELECT
  COUNT(*) AS rows_cnt,
  ROUND(SUM(LENGTH(content))/1024/1024) AS content_mb,
  ROUND(SUM(LENGTH(COALESCE(extra,'')))/1024/1024) AS extra_mb,
  ROUND(SUM(LENGTH(COALESCE(request_json,'')))/1024/1024) AS req_mb,
  ROUND(SUM(LENGTH(COALESCE(response_json,'')))/1024/1024) AS resp_mb
FROM messages;"
```

> 清理前：req_mb + resp_mb 占大头（估算 >1GB），content 仅 ~20MB。

| 字段 | 清理前（估算） | 清理后 | 说明 |
|------|---------------|--------|------|
| request_json | ~1.2GB | 0 | 完整 LLM 请求（system prompt + history + tools） |
| response_json | ~50MB | 0 | LLM 响应原始 JSON |
| content | ~20MB | ~20MB | 消息文本 |
| extra | ~116MB | ~116MB | 工具调用元数据/usage trace |

### 3.5 binlog 历史

```bash
ls -la /var/lib/mysql/ | grep -i bin
```

binlog 文件日增 30~105MB，7 月 10 日至 8 月 8 日累计 ~2.5GB。每天按 UTC 00:00 切表（`max_binlog_size=100M`），频繁切换也是额外 IO。

## 四、根因分析

### 写入放大链路

```
用户发消息
  → LLM 调用（response JSON ≈ 10-60KB，包含完整上下文）
    → react_loop / orchestrator 将 request_json + response_json 全量写入 messages 表
      → MySQL INSERT row（data ≈ 128KB/行）
        → binlog ROW 格式记录完整行镜像 → sync_binlog=1 触发 fsync
        → redo log 写入 → innodb_flush_log_at_trx_commit=1 再次 fsync
  循环：每轮迭代一次上述流程，max_iterations=80
  → 一次复杂对话 = 数十次 DB INSERT + 数十次 fsync
  → 云盘 IOPS 突发额度在高峰期耗尽 → IO 受限
```

### 四个根因，按影响排序

| 排序 | 根因 | 影响 |
|------|------|------|
| 1 | **每轮 LLM 迭代全量持久化 request_json/response_json** | messages 表 128KB/行，1.5G 总量；每次对话写入放大数倍 |
| 2 | `sync_binlog=1` + `binlog_format=ROW` | 每次事务 commit 触发 fsync，ROW 格式复制完整大字段行 |
| 3 | `innodb_flush_log_at_trx_commit=1` | 每次事务额外一次 redo fsync |
| 4 | `innodb_buffer_pool_size=128M` | 2G 机器 MySQL 只拿到 128MB 缓存，读频繁走 OS 页缓存/磁盘 |

## 五、修复方案

### 5.1 MySQL 参数（立即止血，无需改代码）

**修改 `/etc/mysql/mysql.conf.d/mysqld.cnf`**，在 `[mysqld]` 段增加：

```ini
sync_binlog = 0
innodb_flush_log_at_trx_commit = 2
innodb_buffer_pool_size = 402653184
binlog_expire_logs_seconds = 604800
```

| 参数 | 原值 | 新值 | 效果 | 代价 |
|------|------|------|------|------|
| `sync_binlog` | 1 | **0** | 每次事务省掉一次 binlog fsync | 断电丢最近 1 秒日记 |
| `innodb_flush_log_at_trx_commit` | 1 | **2** | redo 从每事务 fsync 改为每秒合并一次 | 断电丢最近 1 秒草稿 |
| `innodb_buffer_pool_size` | 128M | **384M** | MySQL 缓存扩大 3 倍，读少走盘 | 内存多占 ~250M（与可用 1.1G 之和仍安全） |
| `binlog_expire_logs_seconds` | 2592000 (30d) | **604800 (7d)** | 旧 binlog 自动清理，不另行堆盘 | 无法恢复到 7 天前的任意时间点 |

**风险**：三个参数都是"断电丢最近 1 秒"——对单用户个人服务，丢失 1 秒内的事务安全无虞（且没有从库）。

重启 MySQL 后验证：
```bash
mysql -e "SHOW VARIABLES WHERE Variable_name IN ('sync_binlog','innodb_flush_log_at_trx_commit','innodb_buffer_pool_size','binlog_expire_logs_seconds');"
```

### 5.2 代码层（关闭 debug JSON 持久化）

**新增配置** `llm.debug_persist_json`（默认 `false`）：

- `config/config.go`：`LLMConfig` 结构加 `DebugPersistJSON bool`
- `config.yaml`：`debug_persist_json: false`（生产默认关）
- `config.dev.yaml`：`debug_persist_json: true`（开发保留）

**受控落库点**：

| 落库点 | 文件 | 控制方式 |
|--------|------|----------|
| `messages.request_json`（assistant 消息） | `orchestrator.go:saveAssistantMsgWithFlags` | `Orchestrator.debugPersistJSON` 开关 |
| `messages.request_json`（verify 反馈） | `orchestrator.go:saveVerifyFeedback` | 同上 |
| `reflection_llm_calls.request_json/response_json` | `reflection_trace_repo.go:CreateLLMCall` | `ReflectionTraceRepo.persistDebug` 单点收口，所有 react_loop/scorer/secretary 全走此入口 |
| `conversation_summaries.request_json/response_json` | `summary_worker.go` | `SummaryWorker.persistDebug` 开关 |

**影响**：开关关闭后，新的 assistant 消息、反思调用记录、对话摘要的记录中不再包含 LLM 完整请求/响应 JSON。token 用量等功能不受影响。Debug 页面的「完整请求/响应 JSON」功能在线上不可用（dev 环境不受影响）。

**测试更新**：`orchestrator_verify_test.go` 中 verify 反馈消息的断言改为显式开启 `DebugPersistJSON: true`。

### 5.3 存量清理（一次性释放已有磁盘空间）

```sql
UPDATE messages SET request_json = NULL, response_json = NULL;
DELETE FROM reflection_llm_calls WHERE created_at < '2026-08-01';
OPTIMIZE TABLE messages, reflection_llm_calls, embeddings;
PURGE BINARY LOGS BEFORE NOW() - INTERVAL 7 DAY;
```

**清理后效果**：

| 指标 | 清理前 | 清理后 | 降幅 |
|------|--------|--------|------|
| messages 物理大小 | ~1.5GB | 318MB | ↓79% |
| reflection_llm_calls | ~145MB | 29MB | ↓80% |
| MySQL datadir | 4.9GB | 3.4GB | ↓31% |
| / 使用率 | 72% (27G/40G) | 58% (22G/40G) | ↓14% |
| binlog 存量 | ~2.5GB | ~1GB | ↓60% |

### 5.4 附加修复：dump 脚本自动清理

**问题**：`dump-db.sh` 每天 03:00 cron 执行 + 每次部署也执行，从不清理旧备份。`/path/to/xhan-helper/mysqldumps/` 堆积了 16 份备份 = 6.1GB。

**修复**：`scripts/dump-db.sh` 新增 `[keep_count]` 参数（默认 3），dump 完成后自动删除旧于最近 N 份的备份。

## 六、涉及文件

| 文件 | 改动类型 |
|------|----------|
| `server/internal/config/config.go` | `LLMConfig` 加 `DebugPersistJSON` |
| `server/conf/config.yaml` | 加 `debug_persist_json: false` |
| `server/conf/config.dev.yaml` | 加 `debug_persist_json: true` |
| `server/internal/agent/orchestrator.go` | `Orchestrator` 加字段；`saveAssistantMsgWithFlags` 开关；`saveVerifyFeedback` 开关 |
| `server/internal/app/init_repos.go` | `NewReflectionTraceRepo` 传开关 |
| `server/internal/service/reflection_trace_repo.go` | 加 `persistDebug` 字段；`CreateLLMCall` 单点控制 |
| `server/internal/agent/summary_worker.go` | `SummaryWorker` 加 `persistDebug` |
| `server/internal/appinit/agents.go` | `OrchestratorConfig` / `SummaryWorkerConfig` 接线 |
| `server/internal/agent/orchestrator_verify_test.go` | verify 测试显式开启开关 |
| `server/scripts/dump-db.sh` | 自动清理旧备份 |

## 七、经验总结

### 7.1 小规格机器上的 IO 陷阱

- **2C2G 是"恰好够用"的配置**，不能按"多租户云服务"的标准配置参数（sync_binlog=1 和 flush=1 是给金融级场景的）
- 对于单用户个人服务，MySQL 参数可以大胆下降（sync_binlog=0, flush=2），安全性损失忽略不计，IO 收益一个数量级
- `innodb_buffer_pool_size` 默认 128M 严重偏低，应当至少设为系统内存的 15-25%（2G 机器设 384-512M 合理）

### 7.2 大字段写到 DB 的隐蔽性

- `max_tokens=30000` 配合 `max_iterations=80`，单次 LLM 请求 JSON 可达几十 KB —— 每一次落库就是几十次事务写入
- 这些大字段的写入放大（ROW binlog 双写）比普通文本字段高一个数量级
- **"debug 功能"不应成为生产环境的负担**：request_json/response_json 是给开发调试用的，线上默认关掉，仅在 dev 显式开启

### 7.3 排查方法论

1. **先确认瓶颈在哪**：iostat 区分 CPU/IO/网络；监控 vs 自测交叉验证
2. **逐层缩小范围**：归档目录 → 文件大小 → 数据库→ 表 → 列 → 写入路径 → 参数
3. **information_schema 的 data_length 可能不是实时的**：OPTIMIZE 后统计不自动刷新，需 `ANALYZE TABLE` 或直接看物理 .ibd 文件大小

### 7.4 预防清单

- [ ] 新增大字段（`mediumtext`/`longtext`）的持久化必须有开关，生产默认关
- [ ] MySQL 部署后必须检查 `sync_binlog`、`innodb_flush_log_at_trx_commit`、`buffer_pool_size`（不得依赖默认值）
- [ ] 所有 dump/备份脚本必须有自动清理策略（保留最近 N 份），禁止无限增长
- [ ] 定期巡检：`information_schema.tables` Top20 data_length + `du -sh /var/lib/mysql` + binlog 大小趋势
- [ ] `max_tool_result_chars = -1`（不截断）意味着工具结果全量写入 DB，后续可作为下一步优化点
