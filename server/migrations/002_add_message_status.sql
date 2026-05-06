-- 消息软删除 — 新增 status 字段
-- 使用方法: mysql -u root -p xhan_helper < migrations/002_add_message_status.sql

ALTER TABLE messages ADD COLUMN status INT NOT NULL DEFAULT 1 AFTER response_json;
