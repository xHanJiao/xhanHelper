-- Phase 15: 添加 state_facts 字段，存储 JSONL 格式的原子事实
ALTER TABLE theme_states ADD COLUMN state_facts TEXT AFTER history_state_text;
