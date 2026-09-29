-- xhanHelper 建表 SQL（MySQL）
-- 使用方法: mysql -u root -p xhan_helper < migrations/001_init.sql

CREATE TABLE IF NOT EXISTS users (
    id              VARCHAR(36)  NOT NULL PRIMARY KEY,
    username        VARCHAR(50)  NOT NULL,
    password_hash   VARCHAR(255) NOT NULL,
    created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    status          INT          NOT NULL DEFAULT 0,
    UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_tokens (
    id          VARCHAR(36) NOT NULL PRIMARY KEY,
    user_id     VARCHAR(36) NOT NULL,
    token       VARCHAR(36) NOT NULL,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_token (token),
    KEY idx_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS conversations (
    id                    VARCHAR(36)  NOT NULL PRIMARY KEY,
    user_id               VARCHAR(36)  NOT NULL,
    title                 VARCHAR(200) NOT NULL DEFAULT '新对话',
    is_deleted            TINYINT(1)   NOT NULL DEFAULT 0,
    agent_id              VARCHAR(36)  NOT NULL DEFAULT '',
    last_extracted_msg_id VARCHAR(36)  NULL,
    last_extracted_at     DATETIME     NULL,
    created_at            DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS messages (
    id                VARCHAR(36) NOT NULL PRIMARY KEY,
    conversation_id   VARCHAR(36) NOT NULL,
    user_id          VARCHAR(36) NOT NULL,
    role              VARCHAR(20) NOT NULL,
    content           TEXT        NULL,
    reasoning_content TEXT        NULL,
    tool_calls_json   JSON        NULL,
    tool_call_id      VARCHAR(36) NULL,
    embedding         TEXT        NULL,
    memory_ids       JSON        NULL,
    request_json     JSON        NULL,
    response_json    JSON        NULL,
    created_at        DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_conv_created (conversation_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS bills (
    id           VARCHAR(36)    NOT NULL PRIMARY KEY,
    user_id      VARCHAR(36)    NOT NULL,
    item         VARCHAR(100)   NOT NULL,
    amount       DECIMAL(10,2)  NOT NULL,
    category1    VARCHAR(50)    NOT NULL,
    category2    VARCHAR(50)    NOT NULL,
    consume_type VARCHAR(20)    NOT NULL,
    date         VARCHAR(20)    NOT NULL,
    pay_type     VARCHAR(20)    NOT NULL,
    owner        VARCHAR(50)    NOT NULL,
    remark       VARCHAR(200)   NOT NULL DEFAULT '',
    status       INT            NOT NULL DEFAULT 1,
    created_at   DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_user_id (user_id),
    KEY idx_user_status (user_id, status),
    KEY idx_user_date (user_id, date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_memories (
    id                VARCHAR(36)  NOT NULL PRIMARY KEY,
    user_id           VARCHAR(36)  NOT NULL,
    memory_key        VARCHAR(200) NOT NULL,
    content_hash      VARCHAR(64)  NOT NULL,
    memory_value      TEXT         NOT NULL,
    extract_prompt    TEXT         NULL,
    prompt_hash       VARCHAR(64)  NOT NULL DEFAULT '',
    source            VARCHAR(20)  NOT NULL DEFAULT 'conversation',
    confirm_count     INT          NOT NULL DEFAULT 0,
    last_confirmed_at DATETIME     NULL,
    raw_json         TEXT         NULL,
    embedding         TEXT         NULL,
    created_at        DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_user_content (user_id, content_hash),
    KEY idx_prompt_hash (prompt_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS notes (
    id         VARCHAR(36)  NOT NULL PRIMARY KEY,
    user_id    VARCHAR(36)  NOT NULL,
    topic      VARCHAR(100) NOT NULL,
    content    TEXT         NOT NULL,
    variables  JSON         NULL,
    tags       VARCHAR(500) NOT NULL DEFAULT '',
    embedding  TEXT         NULL,
    created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_user_topic (user_id, topic)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id          VARCHAR(36) NOT NULL PRIMARY KEY,
    user_id     VARCHAR(36) NOT NULL,
    tool_name   VARCHAR(50) NOT NULL,
    func_name   VARCHAR(50) NOT NULL,
    params_json JSON        NULL,
    cron_expr   VARCHAR(50) NOT NULL,
    enabled     TINYINT(1)  NOT NULL DEFAULT 1,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agents (
    id            VARCHAR(36)  NOT NULL PRIMARY KEY,
    user_id       VARCHAR(36)  NOT NULL,
    name          VARCHAR(50)  NOT NULL,
    system_prompt TEXT         NOT NULL,
    model         VARCHAR(50)  NOT NULL DEFAULT '',
    created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;


CREATE TABLE IF NOT EXISTS tool_registry (
    id          VARCHAR(36) NOT NULL PRIMARY KEY,
    name        VARCHAR(100) NOT NULL,
    description TEXT NOT NULL,
    schema_json JSON NOT NULL,
    script_path VARCHAR(500) NOT NULL DEFAULT '',
    status      VARCHAR(20) NOT NULL DEFAULT 'draft',
    user_id     VARCHAR(36) NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_name (name),
    KEY idx_user (user_id),
    KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tool_data (
    id          VARCHAR(36) NOT NULL PRIMARY KEY,
    tool_name   VARCHAR(100) NOT NULL,
    func_name   VARCHAR(100) NOT NULL,
    input_json  JSON NOT NULL,
    input_hash  VARCHAR(64) NOT NULL,
    output_json JSON NOT NULL,
    user_id     VARCHAR(36) NOT NULL,
    cached      TINYINT(1) NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_hash (tool_name, func_name, input_hash),
    KEY idx_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_prompts (
    id            VARCHAR(36) NOT NULL PRIMARY KEY,
    user_id       VARCHAR(36) NOT NULL,
    prompt_type   VARCHAR(20) NOT NULL,
    system_prompt TEXT NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_user_type (user_id, prompt_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
