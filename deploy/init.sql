-- DeepSeek Web API 数据库初始化脚本（MySQL 8.0）
-- 用法: mysql -u root -p < deploy/init.sql
-- GORM AutoMigrate 也能建表，本脚本供生产显式管理。

CREATE DATABASE IF NOT EXISTS deepseek_api
    CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE deepseek_api;

-- DeepSeek 账号
CREATE TABLE IF NOT EXISTS accounts (
    id              INT AUTO_INCREMENT PRIMARY KEY,
    name            VARCHAR(64) NOT NULL UNIQUE,
    storage_path    VARCHAR(256) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active/disabled/expired',
    default_model   VARCHAR(64) NOT NULL DEFAULT 'deepseek-chat',
    last_used_at    DATETIME NULL,
    last_check_at   DATETIME NULL,
    note            TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 客户端 API Key
CREATE TABLE IF NOT EXISTS api_keys (
    id              INT AUTO_INCREMENT PRIMARY KEY,
    name            VARCHAR(64) NOT NULL,
    key_prefix      VARCHAR(16) NOT NULL COMMENT '前8位用于展示与定位',
    key_hash        VARCHAR(128) NOT NULL UNIQUE COMMENT 'bcrypt 哈希',
    quota_per_day   INT DEFAULT 1000,
    allowed_models  JSON COMMENT '["deepseek-chat","deepseek-reasoner"]',
    default_model   VARCHAR(64) NOT NULL DEFAULT '',
    enabled         TINYINT(1) DEFAULT 1,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_prefix (key_prefix)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 对话记录
CREATE TABLE IF NOT EXISTS conversations (
    id              CHAR(36) PRIMARY KEY,
    api_key_id      INT,
    account_id      INT,
    model           VARCHAR(64),
    prompt          TEXT NOT NULL,
    reply           LONGTEXT,
    prompt_tokens   INT,
    reply_tokens    INT,
    duration_ms     INT,
    status          VARCHAR(16) COMMENT 'success/failed/streaming',
    error           TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_apikey_time (api_key_id, created_at),
    INDEX idx_account_time (account_id, created_at),
    CONSTRAINT fk_conv_apikey FOREIGN KEY (api_key_id) REFERENCES api_keys(id),
    CONSTRAINT fk_conv_account FOREIGN KEY (account_id) REFERENCES accounts(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 调用统计
CREATE TABLE IF NOT EXISTS usage_hourly (
    api_key_id  INT,
    hour        DATETIME,
    success     INT DEFAULT 0,
    failed      INT DEFAULT 0,
    PRIMARY KEY (api_key_id, hour)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 管理员
CREATE TABLE IF NOT EXISTS admins (
    id            INT AUTO_INCREMENT PRIMARY KEY,
    username      VARCHAR(64) NOT NULL UNIQUE,
    password_hash VARCHAR(128) NOT NULL COMMENT 'bcrypt',
    role          VARCHAR(16) NOT NULL DEFAULT 'superadmin',
    enabled       TINYINT(1) NOT NULL DEFAULT 1,
    token_version INT UNSIGNED NOT NULL DEFAULT 1,
    last_login_at DATETIME NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_admin_role (role)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 管理操作审计日志（不记录请求体或密码）
CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    admin_id    INT UNSIGNED NOT NULL,
    admin_name  VARCHAR(64) NOT NULL,
    action      VARCHAR(32) NOT NULL,
    resource    VARCHAR(64) NOT NULL,
    resource_id VARCHAR(64),
    method      VARCHAR(10) NOT NULL,
    path        VARCHAR(255) NOT NULL,
    client_ip   VARCHAR(64),
    status      INT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_audit_admin (admin_id),
    INDEX idx_audit_action (action),
    INDEX idx_audit_resource (resource),
    INDEX idx_audit_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 已归档的管理审计日志；保留原始 audit_logs.id，确保归档任务可幂等重试
CREATE TABLE IF NOT EXISTS audit_log_archives (
    id          BIGINT UNSIGNED PRIMARY KEY,
    admin_id    BIGINT UNSIGNED NOT NULL,
    admin_name  VARCHAR(64) NOT NULL,
    action      VARCHAR(32) NOT NULL,
    resource    VARCHAR(64) NOT NULL,
    resource_id VARCHAR(64),
    method      VARCHAR(10) NOT NULL,
    path        VARCHAR(255) NOT NULL,
    client_ip   VARCHAR(64),
    status      INT NOT NULL,
    created_at  DATETIME(3) NOT NULL,
    archived_at DATETIME(3) NOT NULL,
    INDEX idx_audit_archive_admin (admin_id),
    INDEX idx_audit_archive_action (action),
    INDEX idx_audit_archive_resource (resource),
    INDEX idx_audit_archive_created (created_at),
    INDEX idx_audit_archived_at (archived_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
