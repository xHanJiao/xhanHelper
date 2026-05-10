-- Token 过期机制
-- 使用方法: mysql -u root -p xhan_helper < migrations/003_add_token_expiry.sql

ALTER TABLE user_tokens ADD COLUMN expires_at DATETIME NOT NULL DEFAULT '9999-12-31 23:59:59' AFTER token;
DELETE FROM user_tokens;
