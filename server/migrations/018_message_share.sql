CREATE TABLE IF NOT EXISTS message_shares (
    id VARCHAR(36) PRIMARY KEY,
    token VARCHAR(64) NOT NULL UNIQUE,
    conversation_id VARCHAR(36) NOT NULL,
    message_ids MEDIUMTEXT NOT NULL,
    created_by VARCHAR(36) NOT NULL,
    expires_at DATETIME NOT NULL,
    visit_count BIGINT DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_token (token),
    INDEX idx_created_by (created_by),
    INDEX idx_conversation_id (conversation_id)
);
