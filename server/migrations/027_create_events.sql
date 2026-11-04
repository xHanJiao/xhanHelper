-- 027: 事件表（记忆聚合）+ user_memories.event_id
-- 事件不存正文：正文是 user_memories.event_id 指向它的记忆；合并/删除时由 EventRepo 搬移或置空。
-- 与 AutoMigrate 双保险：云端 AutoMigrate 失败只打日志继续启动，这里保证 schema 一定就绪。
-- 执行顺序：先跑本 SQL，再启动新二进制；反过来 CREATE TABLE 会因表已存在而中断。
CREATE TABLE events (
  id             VARCHAR(36)  NOT NULL PRIMARY KEY,
  user_id        VARCHAR(36)  NOT NULL,
  name           VARCHAR(100) NOT NULL,
  summary        TEXT,
  is_deleted     TINYINT(1)   NOT NULL DEFAULT 0,
  last_memory_at DATETIME(3)  NULL,
  created_at     DATETIME(3)  NULL,
  updated_at     DATETIME(3)  NULL,
  INDEX idx_events_user_deleted (user_id, is_deleted, last_memory_at)
);

ALTER TABLE user_memories ADD COLUMN event_id VARCHAR(36) NULL;
CREATE INDEX idx_user_memories_event_id ON user_memories (event_id);
