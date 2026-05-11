-- xhanHelper 向量表迁移
-- 阶段1：创建新表
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

-- 阶段2：迁移数据（执行前确认已有数据）
-- 迁移 messages.embedding
-- INSERT INTO embeddings (id, entity_type, entity_id, user_id, model, embedding, created_at, updated_at)
-- SELECT UUID(), 'message', id, user_id, 'doubao-embedding', embedding, created_at, NOW()
-- FROM messages
-- WHERE embedding IS NOT NULL;

-- 迁移 user_memories.embedding
-- INSERT INTO embeddings (id, entity_type, entity_id, user_id, model, embedding, created_at, updated_at)
-- SELECT UUID(), 'memory', id, user_id, 'doubao-embedding', embedding, created_at, NOW()
-- FROM user_memories
-- WHERE embedding IS NOT NULL;

-- 迁移 notes.embedding
-- INSERT INTO embeddings (id, entity_type, entity_id, user_id, model, embedding, created_at, updated_at)
-- SELECT UUID(), 'note', id, user_id, 'doubao-embedding', embedding, created_at, NOW()
-- FROM notes
-- WHERE embedding IS NOT NULL;

-- 阶段3：删除旧字段（确认迁移成功后再执行！）
-- ALTER TABLE messages DROP COLUMN embedding;
-- ALTER TABLE user_memories DROP COLUMN embedding;
-- ALTER TABLE notes DROP COLUMN embedding;
