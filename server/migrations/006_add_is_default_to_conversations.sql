ALTER TABLE conversations ADD COLUMN is_default TINYINT(1) NOT NULL DEFAULT 0 AFTER is_deleted;
