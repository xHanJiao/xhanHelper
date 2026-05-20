-- Phase 7 主题生命周期重构：数据迁移
-- candidate → active（已投递过的就当用户采纳了）
-- archived → closed（合并到 close 状态）
-- 旧 candidate_theme 信箱项标记为已读

-- 1. 主题状态迁移
UPDATE theme_states SET status = 'active' WHERE status = 'candidate';
UPDATE theme_states SET status = 'closed' WHERE status = 'archived';

-- 2. 旧候选主题信箱项不再展示
UPDATE mailbox_items SET is_read = 1 WHERE type = 'candidate_theme';
