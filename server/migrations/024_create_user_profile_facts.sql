-- 024: 用户画像事实表（field → fact 三级结构，替代 user_profile 单值表）
-- 延迟迁移：旧 user_profile 数据由首次增量提取时 LLM 拆分写入本表，本迁移不做数据搬移
CREATE TABLE user_profile_facts (
  id         VARCHAR(36) NOT NULL PRIMARY KEY,
  user_id    VARCHAR(36) NOT NULL,
  field      VARCHAR(64) NOT NULL,
  value      TEXT NOT NULL,
  status     VARCHAR(16) NOT NULL DEFAULT 'active',
  batch_id   VARCHAR(36) NOT NULL,
  created_at DATETIME NOT NULL,
  INDEX idx_facts_user_field (user_id, field, status),
  INDEX idx_facts_batch (batch_id)
);
