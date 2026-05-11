# 向量存储改造方案

## 一、目标

把分散在 `messages`/`user_memories`/`notes` 表中的 `embedding` 字段抽取到统一的 `embeddings` 表，使用 TEXT 格式存储 JSON 数组。

---

## 二、数据库设计

### 2.1 新建 `embeddings` 表

```sql
CREATE TABLE IF NOT EXISTS embeddings (
    id           VARCHAR(36)  NOT NULL PRIMARY KEY,
    entity_type  VARCHAR(20)  NOT NULL COMMENT '实体类型: message|note|memory',
    entity_id    VARCHAR(36)  NOT NULL COMMENT '实体ID',
    user_id      VARCHAR(36)  NOT NULL COMMENT '用户ID',
    model        VARCHAR(100) NOT NULL COMMENT '生成向量的模型',
    embedding    TEXT         NOT NULL COMMENT 'JSON 数组向量数据',
    created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_entity (entity_type, entity_id),
    KEY idx_user_type (user_id, entity_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2.2 迁移数据 SQL

```sql
-- 迁移 messages.embedding
INSERT INTO embeddings (id, entity_type, entity_id, user_id, model, embedding, created_at, updated_at)
SELECT UUID(), 'message', id, user_id, 'doubao-embedding', embedding, created_at, NOW()
FROM messages
WHERE embedding IS NOT NULL;

-- 迁移 user_memories.embedding
INSERT INTO embeddings (id, entity_type, entity_id, user_id, model, embedding, created_at, updated_at)
SELECT UUID(), 'memory', id, user_id, 'doubao-embedding', embedding, created_at, NOW()
FROM user_memories
WHERE embedding IS NOT NULL;

-- 迁移 notes.embedding
INSERT INTO embeddings (id, entity_type, entity_id, user_id, model, embedding, created_at, updated_at)
SELECT UUID(), 'note', id, user_id, 'doubao-embedding', embedding, created_at, NOW()
FROM notes
WHERE embedding IS NOT NULL;
```

### 2.3 删除旧字段 SQL

```sql
-- 注意：先确认数据迁移成功再执行！
ALTER TABLE messages DROP COLUMN embedding;
ALTER TABLE user_memories DROP COLUMN embedding;
ALTER TABLE notes DROP COLUMN embedding;
```

### 2.4 Migration 文件

新建 `migrations/004_extract_embeddings.sql`，包含以上 SQL（分阶段）。

---

## 三、代码改动

### 3.1 新增 `internal/model/embedding.go`

```go
package model

import "time"

// EntityType 实体类型
const (
	EntityTypeMessage = "message"
	EntityTypeNote    = "note"
	EntityTypeMemory  = "memory"
)

// Embedding — 向量存储
type Embedding struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	EntityType string    `gorm:"uniqueIndex:idx_entity,priority:1;size:20;not null" json:"entity_type"`
	EntityID   string    `gorm:"uniqueIndex:idx_entity,priority:2;size:36;not null" json:"entity_id"`
	UserID     string    `gorm:"index:idx_user_type,priority:1;size:36;not null" json:"user_id"`
	Model      string    `gorm:"size:100;not null" json:"model"`
	Embedding  *string   `gorm:"type:text" json:"-"` // JSON 数组向量数据
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
```

### 3.2 修改现有 Model

**`internal/model/message.go`**：删除 `Embedding *string` 字段

**`internal/model/note.go`**：删除 `Embedding *string` 字段

**`internal/model/user_memory.go`**：删除 `Embedding *string` 字段

---

### 3.3 修改 `internal/llm/embedding.go`

继续使用现有的序列化函数，无需改动：

```go
// EmbeddingToString 向量序列化为 JSON 字符串
func EmbeddingToString(vec []float64) string { ... }

// StringToEmbedding JSON 字符串反序列化为向量
func StringToEmbedding(s string) ([]float64, error) { ... }
```

---

### 3.4 新增 `internal/service/embedding_repo.go`

```go
package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// EmbeddingRepo — 向量数据访问
type EmbeddingRepo struct {
	db *gorm.DB
}

// NewEmbeddingRepo 创建 EmbeddingRepo
func NewEmbeddingRepo(db *gorm.DB) *EmbeddingRepo {
	return &EmbeddingRepo{db: db}
}

// Upsert 创建或更新向量
func (r *EmbeddingRepo) Upsert(ctx context.Context, emb *model.Embedding) error {
	// 先尝试更新，不存在则插入
	var existing model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", emb.EntityType, emb.EntityID).
		First(&existing).Error

	if err == nil {
		// 更新
		emb.ID = existing.ID
		emb.CreatedAt = existing.CreatedAt
		return r.db.WithContext(ctx).Model(&existing).Updates(emb).Error
	}

	// 插入
	return r.db.WithContext(ctx).Create(emb).Error
}

// GetByEntity 获取单个实体的向量
func (r *EmbeddingRepo) GetByEntity(ctx context.Context, entityType, entityID string) (*model.Embedding, error) {
	var emb model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		First(&emb).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &emb, err
}

// ListByType 按类型批量获取向量（用于 warmup）
func (r *EmbeddingRepo) ListByType(ctx context.Context, entityType string, limit, offset int) ([]model.Embedding, error) {
	var list []model.Embedding
	err := r.db.WithContext(ctx).
		Where("entity_type = ?", entityType).
		Limit(limit).Offset(offset).
		Find(&list).Error
	return list, err
}

// DeleteByEntity 删除实体的向量
func (r *EmbeddingRepo) DeleteByEntity(ctx context.Context, entityType, entityID string) error {
	return r.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Delete(&model.Embedding{}).Error
}
```

---

### 3.5 修改现有 Repo

#### 3.5.1 `internal/service/note_repo.go`

- 删除 `UpdateEmbedding` 方法
- 修改 `ListAllPaginated`：不再需要加载 embedding 字段

#### 3.5.2 `internal/service/user_memory_repo.go`

- 删除 `UpdateEmbedding` 方法
- 修改 `ListAllUsersMemoriesPaginated`：不再需要加载 embedding 字段

#### 3.5.3 `internal/service/message_repo.go`

- 删除 `UpdateLastUserEmbedding` 方法

---

### 3.6 修改 `internal/agent/` 下的文件

#### 3.6.1 `internal/agent/note_source.go`

- Warmup 逻辑改为从 `EmbeddingRepo.ListByType("note", ...)` 加载
- 调用 `llm.StringToEmbedding` 反序列化

#### 3.6.2 `internal/agent/memory_source.go`

- Warmup 逻辑改为从 `EmbeddingRepo.ListByType("memory", ...)` 加载
- 调用 `llm.StringToEmbedding` 反序列化

#### 3.6.3 `internal/agent/orchestrator.go`

- `saveUserMsgEmbedding` 方法改为使用 `EmbeddingRepo.Upsert`，`entity_type="message"`

#### 3.6.4 `internal/agent/extractor.go`

- 保存记忆向量改为使用 `EmbeddingRepo.Upsert`，`entity_type="memory"`

---

### 3.7 修改 `internal/tool/note.go`

- 保存笔记向量改为使用 `EmbeddingRepo.Upsert`，`entity_type="note"`

---

### 3.8 修改 `cmd/xhan-helper/main.go`

- 初始化 `EmbeddingRepo`
- 注入到 `Orchestrator`/`NoteSource`/`MemorySource`/`NoteTool`/`MemoryExtractor` 等需要的地方

---

## 四、数据迁移策略

### 4.1 迁移步骤

1. **部署新代码（但不执行删除列操作）**
   - 新代码同时支持读写旧表和新表
   - 写操作只写新表
   - 读操作优先读新表，没有 fallback 到旧表

2. **后台迁移数据**
   - 执行 migration SQL 把旧数据复制到新表

3. **验证无误后，删除旧列**

### 4.2 兼容层（可选）

如果担心数据一致性，可以加一个兼容层：

- 读：先查新表，没有则查旧表，如果旧表有则自动迁移到新表
- 写：只写新表

---

## 五、文件清单

### 新增文件

| 文件 | 说明 |
|------|------|
| `migrations/004_extract_embeddings.sql` | 建表 + 迁移 SQL |
| `internal/model/embedding.go` | Embedding 模型 |
| `internal/service/embedding_repo.go` | EmbeddingRepo |

### 修改文件

| 文件 | 改动 |
|------|------|
| `internal/model/message.go` | 删除 Embedding 字段 |
| `internal/model/note.go` | 删除 Embedding 字段 |
| `internal/model/user_memory.go` | 删除 Embedding 字段 |
| `internal/service/note_repo.go` | 删除 UpdateEmbedding |
| `internal/service/user_memory_repo.go` | 删除 UpdateEmbedding |
| `internal/service/message_repo.go` | 删除 UpdateLastUserEmbedding |
| `internal/agent/note_source.go` | Warmup 改为从 EmbeddingRepo 加载 |
| `internal/agent/memory_source.go` | Warmup 改为从 EmbeddingRepo 加载 |
| `internal/agent/orchestrator.go` | saveUserMsgEmbedding 用 EmbeddingRepo |
| `internal/agent/extractor.go` | 保存记忆向量用 EmbeddingRepo |
| `internal/tool/note.go` | 保存笔记向量用 EmbeddingRepo |
| `cmd/xhan-helper/main.go` | 初始化 EmbeddingRepo 并注入 |

---

## 六、实施顺序

1. 写好 migration SQL
2. 写好 `model/embedding.go` 和 `service/embedding_repo.go`
3. 逐个修改 agent/tool 里的调用点
4. 修改 main.go 注入依赖
5. 本地测试（使用现有数据测试迁移和功能）
6. 部署
