-- 4.3 会话模式：plan（只读讨论）/ work（默认）
ALTER TABLE conversations ADD COLUMN mode VARCHAR(10) NOT NULL DEFAULT 'work';
