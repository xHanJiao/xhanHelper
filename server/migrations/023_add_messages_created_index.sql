-- 023: messages.created_at 单列索引
-- 支撑 debug_json 保留清理按时间分批扫描（全局 created_at 范围查询，复合索引 idx_conv_created 无法满足）
CREATE INDEX idx_messages_created ON messages (created_at);
