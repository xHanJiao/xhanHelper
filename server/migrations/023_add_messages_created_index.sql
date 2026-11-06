-- 023: messages(created_at, role) 复合索引
-- 支撑 debug_json / extra trace 保留清理按时间分批扫描，以及 unified_search history
-- 关键词检索的时间窗裁剪（原单列 idx_messages_created 的 created_at 是新索引的前缀）。
--
-- 2026-11-06: 生产 SHOW INDEX 确认旧单列索引从未应用；若某环境手工应用过旧版本，
-- 先 ALTER TABLE messages DROP INDEX idx_messages_created; 再执行本文件。
CREATE INDEX idx_messages_created_role ON messages (created_at, role);
