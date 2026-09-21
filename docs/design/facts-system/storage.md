# 事实系统 · 存储层 — 实现设计

> 版本：v1 | 日期：2026-09-21 | 状态：设计定稿，待开发
> 上级文档：[design.md](./design.md)（总览）
> 估算（定稿）：4 面 / 2 缝（0 条探针）/ 0 次真机往返 / 0 个待拍决定 → 2–3 小时

本文件是总览文档「存到哪里」一节的下探：表结构、repo、向量契约、启动预热、迁移接线。只落地存储层，抽取 / 决策 / worker 的写法在各自文档中展开；本文只把它们的消费接口留出来。

---

## 1. 复用的现有设施

存储层几乎全部复用现有设施，不新造轮子。

| 现有 | 位置 | 复用方式 |
|---|---|---|
| `Embedding` 表 | `model/embedding.go:17` | `EntityType="fact"`、`EntityID=fact.ID`、`ID=fact.ID`（与 memory 同款） |
| `EmbeddingRepo` | `service/embedding_repo.go` | `Upsert` / `GetByEntity` / `ListByEntities` / `DeleteByEntity` **全部现成，零改动** |
| 内存向量 | `agent/vector_store.go` + `app.VectorStore`（`app/vector.go:16`） | key `fact:<id>`，metadata 带 type/user_id/subject/kind/content |
| `EntityType` 常量块 | `model/embedding.go:6` | 加一行 `EntityTypeFact = "fact"` |
| `idgen.Next()` | `idgen/idgen.go:30` | changelog 主键。返回 string，与 `LLMCallIO.ID`（`model/llm_call_io.go:11`，`size:36`）同款 |
| `uuid.New()` | 已在 `chunk_builder.go:110` 等使用 | facts 主键 |
| `WarmupAll` | `agent/warmup.go:15` | 加 `warmupFactEmbeddings` 分支 |
| 分页约定 | `user_memory_repo.go:112` | repo 所有 list 方法带 limit/offset，`limit<=0` 给默认值 |
| sqlite 内存库测试 | `service/user_profile_fact_repo_test.go:12` | repo 单测范本 |
| `UserProfileFact` status 软删 | `model/user_profile_fact.go:22` | status 常量 + 软删语义对照 |

## 2. 数据模型

### 2.1 `model/fact.go`

```go
package model

import "time"

// Fact — 单条长期事实。content 是嵌入/注入/比对的三重单位。
type Fact struct {
    ID          string    `gorm:"primaryKey;size:36" json:"id"`
    UserID      string    `gorm:"index:idx_fact_user_subject;index:idx_fact_user_hash;size:36;not null" json:"user_id"`
    Subject     string    `gorm:"index:idx_fact_user_subject;size:200;not null;default:''" json:"subject"`
    Kind        string    `gorm:"size:16;not null" json:"kind"`
    Content     string    `gorm:"type:text;not null" json:"content"`
    ContentHash string    `gorm:"index:idx_fact_user_hash;size:64;not null" json:"content_hash"`
    Fields      *string   `gorm:"type:json" json:"fields,omitempty"`
    Status      string    `gorm:"size:16;not null;default:active" json:"status"`
    CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
    UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// FactKind — 事实类型。只统计，不参与检索。
const (
    FactKindState = "state" // 当前状态/身份/关系/计划/判断，会被后续事实取代
    FactKindEvent = "event" // 已发生的事件/观点，只增不改
)

// FactStatusDeleted 软删状态。active 复用 UserProfileFact 的 FactStatusActive（同语义，不重复声明）。
const FactStatusDeleted = "deleted"
```

设计要点：

- **用 `status` 软删，不用 `gorm.DeletedAt`**：对齐 `UserProfileFact`；DELETE 要留 changelog；未来回滚要能读回被删行。
- **`Fields` 是可空 JSON 列**（口子，见 §7）：`due_at` / `has_doc_ref` 先存不注入，将来加字段不用迁移。
- **`Kind` / `Status` 校验放写侧**（用上面的常量），DB 不加 CHECK 约束——与项目现有表风格一致。
- **索引**：`(user_id, subject)` 供未来按 subject 分组统计；`(user_id, content_hash)` 供精确判重。
  - GORM 复合索引按字段定义顺序排列：`UserID` 在 `Subject`/`ContentHash` 之前，故实际为 `(user_id, subject)` 与 `(user_id, content_hash)`，与 SQL 预期一致。
- **不建 chunk**：facts 是单句，`content` 全文向量即检索单位，无需 `chunk_builder`。

### 2.2 `model/fact_changelog.go`

```go
package model

import "time"

// FactChangelog — 事实变更审计。仅 UPDATE / DELETE 记录（ADD 无前值）。
type FactChangelog struct {
    ID          string    `gorm:"primaryKey;size:36" json:"id"` // idgen.Next()
    UserID      string    `gorm:"size:36;not null;index:idx_factlog_user" json:"user_id"`
    FactID      string    `gorm:"size:36;not null;index:idx_factlog_fact" json:"fact_id"`
    Op          string    `gorm:"size:16;not null" json:"op"` // update / delete
    OldContent  string    `gorm:"type:text;not null" json:"old_content"`
    NewContent  string    `gorm:"type:text;not null;default:''" json:"new_content"` // 仅 update
    SourceMsgID string    `gorm:"size:36;not null;default:''" json:"source_msg_id"`
    CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// FactChangelogOp — 变更类型。
const (
    FactOpUpdate = "update"
    FactOpDelete = "delete"
)
```

> 总览文档 §10.2 原写 `id BIGINT`，已修正：`idgen.Next()` 返回 string，对齐 `LLMCallIO`。

## 3. Repo 层

### 3.1 `service/fact_repo.go`

风格完全对齐 `service/user_memory_repo.go`：not found 返回 `(nil, nil)`，所有 list 分页。

```go
type FactRepo struct{ db *gorm.DB }

func NewFactRepo(db *gorm.DB) *FactRepo

// Create 插入一条事实。
Create(ctx context.Context, f *model.Fact) error

// FindByID 按 ID + 用户查单条（任意 status，供审计/回滚读回）。not found → (nil, nil)
FindByID(ctx context.Context, userID, id string) (*model.Fact, error)

// FindByContentHash 精确判重：按 user + hash 查 active 事实。not found → (nil, nil)
FindByContentHash(ctx context.Context, userID, contentHash string) (*model.Fact, error)

// UpdateContent 覆盖 content（及可选 subject/kind）。
UpdateContent(ctx context.Context, id, content, contentHash, subject, kind string) error

// SoftDelete 软删：status = deleted。
SoftDelete(ctx context.Context, id string) error

// ListByUser 分页列表（含总数），供未来 API/Admin。默认 limit 50。
ListByUser(ctx context.Context, userID string, limit, offset int) ([]model.Fact, int64, error)

// ListAllActivePaginated 全用户 active 分页，仅 warmup 用。默认 limit 100。
ListAllActivePaginated(ctx context.Context, limit, offset int) ([]model.Fact, error)
```

实现约束：

- `FindByContentHash` / `ListByUser` / `ListAllActivePaginated` **显式带 `status = active`**；`FindByID` 不过滤（审计要读回 deleted 行）。
- 错误一律 `fmt.Errorf("fact repo xxx: %w", err)` 上抛，`gorm.ErrRecordNotFound` 归一为 `(nil, nil)`——照 `user_memory_repo.go:35` 的写法。
- 更新用 `Updates(map[string]interface{}{...})`，不用 `Save`（避免零值覆盖）。

### 3.2 `service/fact_changelog_repo.go`

```go
type FactChangelogRepo struct{ db *gorm.DB }

func NewFactChangelogRepo(db *gorm.DB) *FactChangelogRepo

// Create 写入一条变更记录。
Create(ctx context.Context, c *model.FactChangelog) error

// ListByFact 按事实分页列出变更（含总数）。默认 limit 20。
ListByFact(ctx context.Context, userID, factID string, limit, offset int) ([]model.FactChangelog, int64, error)
```

### 3.3 向量行（不改 EmbeddingRepo）

facts 的向量行直接复用 `EmbeddingRepo`：

- 写/改：`embeddingRepo.Upsert(ctx, &model.Embedding{ID: fact.ID, EntityType: "fact", EntityID: fact.ID, UserID: ..., Model: embedder.ModelName(), Embedding: &embStr})`
- 删：`embeddingRepo.DeleteByEntity(ctx, "fact", fact.ID)`

`(entity_type, entity_id)` 是 `Embedding` 的唯一索引（`model/embedding.go:19-20`），Upsert 天然幂等。`ID` 与 `EntityID` 同值，与 `storeMemory`（`extractor.go:229`）一致。

## 4. 向量契约与启动预热

### 4.1 契约

| 项 | 值 |
|---|---|
| `Embedding.EntityType` | `model.EntityTypeFact`（`"fact"`） |
| `Embedding.EntityID` / `ID` | `fact.ID` |
| 内存 key | `"fact:" + fact.ID` |
| 内存 metadata | `{type:"fact", user_id, subject, kind, content}` |

metadata 带 `content/subject/kind` 是**有意为之**（口子）：决策段取近邻时直接读 metadata 组装 prompt，**不回表**；未来按 subject 检索/注入也已就位。

### 4.2 `warmupFactEmbeddings`（`agent/warmup.go`）

关键实现细节：`Embedding` 表里没有 subject/kind/content，所以**以 facts 表为驱动**重建内存向量，而不是以 embedding 表驱动再回表补字段。

```
分页 factRepo.ListAllActivePaginated(pageSize, offset)   // 天然只含 active
  → 收集本页 fact IDs
  → embeddingRepo.ListByEntities(ctx, EntityTypeFact, ids) // 现成方法，一次批量取
  → 对每条有向量的 fact：
        llm.StringToEmbedding(*emb.Embedding)
        vectorStore.Upsert("fact:"+f.ID, vec, {type,user_id,subject,kind,content})
```

优点：软删行不进内存；metadata 直接从 fact 行取；零新增 repo 方法。

`WarmupAll` 签名增加 `factRepo *service.FactRepo`：

```go
func WarmupAll(ctx context.Context, vs app.VectorStore, embeddingRepo *service.EmbeddingRepo,
    conceptEmbedRepo *service.ConceptEmbeddingRepo, themeStateRepo *service.ThemeStateRepo,
    factRepo *service.FactRepo, chunkDisabledParents []string)
```

调用点 `appinit/agents.go:47` 同步加参 `repos.Fact`。

## 5. 迁移与接线

| 文件 | 改动 |
|---|---|
| `model/embedding.go` | `EntityType` 常量块加 `EntityTypeFact = "fact"` |
| `app/init_db.go:26` | AutoMigrate 列表加 `&model.Fact{}`、`&model.FactChangelog{}` |
| `app/init_repos.go:11` | `Repos` 加 `Fact *service.FactRepo`、`FactChangelog *service.FactChangelogRepo` + 构造 |
| `agent/warmup.go:15` | 加 `warmupFactEmbeddings`；`WarmupAll` 增参 |
| `appinit/agents.go:47` | `WarmupAll` 调用加 `repos.Fact` |

## 6. 配置

新增顶层 `facts:`。存储层这一刀**不引入 `facts.enabled`**——还没有消费它的 `FactWorker`，等 worker 接线那一刀再加（默认 `false`）。本次只加会被存储/检索消费的三个参数：

```yaml
facts:
  top_k: 5               # 决策段检索近邻数
  dupe_threshold: 0.95   # 向量 top1 静默跳过阈值
  max_candidates: 20     # 单次抽取候选上限
```

`config/config.go` 改动：

- `Config` 加 `Facts FactsConfig \`yaml:"facts"\``
- 新增 `FactsConfig` 结构体
- `merge()` 加三个非零值覆盖
- `validate()`（默认值段）加三个默认值：5 / 0.95 / 20

`conf/config.yaml` 加该段（dev/prod 覆盖文件无需重复，除非要改值）。

## 7. 留的口子

| 口子 | 留给未来 |
|---|---|
| `Fields` 可空 JSON 列 | `due_at` 注入、提醒联动，不用迁移 |
| `Status` 常量（预留 `superseded`） | 未来加状态只加常量，不改表 |
| 向量 metadata 带 `subject/kind/content` | 决策段近邻免回表；未来按 subject 检索/注入 |
| `FactChangelog` 表（含 `old_content`） | 回滚能力的数据基础 |
| `FactRepo.ListByUser` 分页 | 未来 API / Admin 看板 |
| `EntityTypeFact` 常量 | 未来把 facts 注入主对话检索 |
| `warmup` 只加载 active | 软删不占内存 |
| 无 `facts.enabled` | worker 接线时再加，避免空开关 |

## 8. 测试

仿 `service/user_profile_fact_repo_test.go` 的 sqlite `:memory:` 模式。

`service/fact_repo_test.go`：

- `Create` → `FindByID` 命中
- `FindByContentHash` 只命中 active（先软删再查应返回 nil）
- `UpdateContent` 覆盖 content/hash/subject/kind 后 `FindByID` 可见新值
- `SoftDelete` 后 `FindByContentHash` / `ListByUser` / `ListAllActivePaginated` 均不可见，但 `FindByID` 仍可读回
- `ListByUser` 分页：总数与页大小正确

`service/fact_changelog_repo_test.go`：

- `Create` + `ListByFact` 分页、按 fact 过滤

错误处理按 AGENTS 规范：repo 出错 `fmt.Errorf("...: %w", err)` 上抛；not found 用 `(nil, nil)`；顶层调用方记录一次。

## 9. 文件清单

**新增**

| 文件 | 说明 |
|---|---|
| `server/internal/model/fact.go` | Fact 模型 + Kind/Status 常量 |
| `server/internal/model/fact_changelog.go` | FactChangelog 模型 + Op 常量 |
| `server/internal/service/fact_repo.go` | facts 数据访问 |
| `server/internal/service/fact_changelog_repo.go` | changelog 数据访问 |
| `server/internal/service/fact_repo_test.go` | repo 单测 |
| `server/internal/service/fact_changelog_repo_test.go` | repo 单测 |

**修改**

| 文件 | 改动 |
|---|---|
| `server/internal/model/embedding.go` | 加 `EntityTypeFact` |
| `server/internal/app/init_db.go` | AutoMigrate 两张表 |
| `server/internal/app/init_repos.go` | Repos 聚合 |
| `server/internal/agent/warmup.go` | warmupFactEmbeddings |
| `server/internal/appinit/agents.go` | WarmupAll 传参 |
| `server/internal/config/config.go` | FactsConfig + 默认值 + merge |
| `server/conf/config.yaml` | facts 段 |

**验收**：`go build ./... && go test ./...` 全绿。存储层无 LLM 调用、无真机往返需求。
