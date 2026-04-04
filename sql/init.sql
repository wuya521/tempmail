-- ============================================================
-- TempMail 临时邮箱平台 - 数据库初始化
-- 针对高并发优化：索引、分区就绪、UUID主键
-- ============================================================

-- 启用扩展
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ============================================================
-- 1. 账号表 (accounts)
-- ============================================================
CREATE TABLE accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username    VARCHAR(64)  NOT NULL UNIQUE,
    api_key     VARCHAR(64)  NOT NULL UNIQUE,
    is_admin    BOOLEAN      NOT NULL DEFAULT FALSE,
    is_active   BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ
);

-- API Key 查询走 B-tree 索引（认证热路径）
CREATE INDEX idx_accounts_api_key ON accounts (api_key);

-- ============================================================
-- 2. 域名池表 (domains)
-- ============================================================
CREATE TABLE domains (
    id            SERIAL PRIMARY KEY,
    domain        VARCHAR(255) NOT NULL UNIQUE,
    is_active     BOOLEAN      NOT NULL DEFAULT TRUE,
    status        VARCHAR(16)  NOT NULL DEFAULT 'active',  -- active / pending / disabled
    mx_checked_at TIMESTAMPTZ,                             -- 最近一次 MX 检测时间
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_domains_active ON domains (is_active) WHERE is_active = TRUE;
CREATE INDEX idx_domains_status ON domains (status) WHERE status = 'pending';

-- ============================================================
-- 3. 邮箱表 (mailboxes)
-- ============================================================
CREATE TABLE mailboxes (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   UUID         NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    address      VARCHAR(128) NOT NULL,  -- 本地部分，如 "abc123"
    domain_id    INT          NOT NULL REFERENCES domains(id),
    full_address VARCHAR(320) NOT NULL,  -- 完整地址 "abc123@mail.xxx.xyz"
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ  DEFAULT (NOW() + INTERVAL '30 minutes')  -- NULL = 永不过期（TTL=0）
);

-- 完整地址唯一索引（收件匹配热路径）
CREATE UNIQUE INDEX idx_mailboxes_full_address ON mailboxes (full_address);

-- 按账号查邮箱列表
CREATE INDEX idx_mailboxes_account_id ON mailboxes (account_id);

-- 过期自动清理索引
CREATE INDEX idx_mailboxes_expires_at ON mailboxes (expires_at);

-- ============================================================
-- 4. 邮件表 (emails)
-- ============================================================
CREATE TABLE emails (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id   UUID         NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    sender       VARCHAR(320) NOT NULL DEFAULT '',
    subject      VARCHAR(998) NOT NULL DEFAULT '',
    body_text    TEXT         NOT NULL DEFAULT '',
    body_html    TEXT         NOT NULL DEFAULT '',
    raw_message  TEXT         NOT NULL DEFAULT '',
    size_bytes   INT          NOT NULL DEFAULT 0,
    received_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 按邮箱查邮件（分页查询热路径）
CREATE INDEX idx_emails_mailbox_received ON emails (mailbox_id, received_at DESC);

-- ============================================================
-- 5. 初始管理员账号
-- ============================================================
INSERT INTO accounts (username, api_key, is_admin)
VALUES ('admin', 'tm_admin_' || encode(gen_random_bytes(24), 'hex'), TRUE);

-- ============================================================
-- 6. 初始域名（请在启动后通过管理后台或 API 添加实际域名）
-- ============================================================
-- INSERT INTO domains (domain) VALUES ('mail.yourdomain.com');

-- ============================================================
-- 7. 应用设置表 (app_settings)
-- ============================================================
CREATE TABLE IF NOT EXISTS app_settings (
    key        VARCHAR(64) PRIMARY KEY,
    value      TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO app_settings (key, value) VALUES ('registration_open', 'true') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('smtp_server_ip', '') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('smtp_hostname', '') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('mailbox_ttl_minutes', '30') ON CONFLICT DO NOTHING;

-- ============================================================
-- 8. Claude 自助售号（店铺配置、库存、订单）
-- ============================================================
CREATE TABLE claude_shop_config (
    id                      SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    enabled                 BOOLEAN      NOT NULL DEFAULT FALSE,
    title                   VARCHAR(160) NOT NULL DEFAULT 'Claude 成品账号',
    subtitle                VARCHAR(256) NOT NULL DEFAULT '',
    description             TEXT         NOT NULL DEFAULT '',
    tutorial_url            TEXT         NOT NULL DEFAULT '',
    retail_price_cents      INT          NOT NULL DEFAULT 9900,
    wholesale_min_qty       INT          NOT NULL DEFAULT 5,
    wholesale_price_cents   INT          NOT NULL DEFAULT 7900,
    tag_hot                 BOOLEAN      NOT NULL DEFAULT FALSE,
    show_tag_wholesale      BOOLEAN      NOT NULL DEFAULT TRUE,
    tag_fan_welfare         VARCHAR(64)  NOT NULL DEFAULT '',
    max_per_user            INT          NOT NULL DEFAULT 0,
    wechat_qr_file          VARCHAR(255) NOT NULL DEFAULT '',
    alipay_qr_file          VARCHAR(255) NOT NULL DEFAULT '',
    static_payment_manual_confirm BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
INSERT INTO claude_shop_config (id) VALUES (1);

CREATE TABLE claude_orders (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       UUID         NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    quantity         INT          NOT NULL CHECK (quantity >= 1 AND quantity <= 999),
    unit_price_cents INT          NOT NULL,
    total_cents      INT          NOT NULL,
    is_wholesale     BOOLEAN      NOT NULL DEFAULT FALSE,
    status           VARCHAR(32)  NOT NULL DEFAULT 'awaiting_payment',
    payment_channel  VARCHAR(24)  NOT NULL DEFAULT 'static',
    alipay_trade_no  VARCHAR(64),
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    fulfilled_at     TIMESTAMPTZ
);
CREATE INDEX idx_claude_orders_account ON claude_orders (account_id, created_at DESC);
CREATE INDEX idx_claude_orders_status ON claude_orders (status);

CREATE TABLE claude_inventory (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email        VARCHAR(320) NOT NULL,
    api_key      VARCHAR(128) NOT NULL,
    status       VARCHAR(24)  NOT NULL DEFAULT 'available',
    order_id     UUID         REFERENCES claude_orders(id) ON DELETE SET NULL,
    batch_label  VARCHAR(64)  NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_claude_inv_available ON claude_inventory (created_at ASC) WHERE status = 'available';

CREATE TABLE claude_order_lines (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id   UUID        NOT NULL REFERENCES claude_orders(id) ON DELETE CASCADE,
    line_index INT         NOT NULL,
    email      VARCHAR(320) NOT NULL,
    api_key    VARCHAR(128) NOT NULL,
    UNIQUE (order_id, line_index)
);
CREATE INDEX idx_claude_order_lines_order ON claude_order_lines (order_id);

-- ============================================================
-- 9. 数据库性能参数（在 postgresql.conf 或 docker 环境变量中设置更佳）
-- ============================================================
-- 以下通过 ALTER SYSTEM 设置，重启后生效
-- ALTER SYSTEM SET shared_buffers = '256MB';
-- ALTER SYSTEM SET effective_cache_size = '512MB';
-- ALTER SYSTEM SET work_mem = '4MB';
-- ALTER SYSTEM SET maintenance_work_mem = '64MB';
-- ALTER SYSTEM SET max_connections = 200;
