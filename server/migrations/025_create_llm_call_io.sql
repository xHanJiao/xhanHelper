-- 025: LLM 调用审计表（I/O 内容，后处理/agent_summary/reminder_gen）
-- 保留 7 天，清理任务按 created_at 批量删除
CREATE TABLE llm_call_io (
  id                VARCHAR(36) NOT NULL PRIMARY KEY,
  category          VARCHAR(30) NOT NULL,
  sub_category      VARCHAR(50) NOT NULL,
  user_id           VARCHAR(36) NOT NULL DEFAULT '',
  conv_id           VARCHAR(36) NOT NULL DEFAULT '',
  ref_type          VARCHAR(30) NOT NULL DEFAULT '',
  ref_id            VARCHAR(36) NOT NULL DEFAULT '',
  model             VARCHAR(50) NOT NULL,
  system_prompt     TEXT,
  messages_json     TEXT,
  response          TEXT,
  prompt_tokens     BIGINT NOT NULL DEFAULT 0,
  completion_tokens BIGINT NOT NULL DEFAULT 0,
  latency_ms        BIGINT NOT NULL DEFAULT 0,
  error_msg         TEXT,
  created_at        DATETIME NOT NULL,
  INDEX idx_io_created (created_at),
  INDEX idx_io_category (category, sub_category),
  INDEX idx_io_conv (conv_id)
);
