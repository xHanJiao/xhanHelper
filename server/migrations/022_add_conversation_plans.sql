-- 计划模式 todo：每会话一行，steps_json 全量替换
CREATE TABLE IF NOT EXISTS conversation_plans (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    goal VARCHAR(500) NOT NULL DEFAULT '',
    steps_json MEDIUMTEXT NOT NULL,
    updated_at DATETIME(3) NULL,
    INDEX idx_conv_plans_conv (conversation_id),
    INDEX idx_conv_plans_user (user_id)
);
