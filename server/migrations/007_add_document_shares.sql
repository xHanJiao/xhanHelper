CREATE TABLE IF NOT EXISTS document_shares (
    id VARCHAR(36) PRIMARY KEY,
    document_id VARCHAR(36) NOT NULL,
    token VARCHAR(64) NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_document_shares_document_id (document_id),
    UNIQUE INDEX idx_document_shares_token (token)
);
