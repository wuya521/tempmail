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
    last_seen_at TIMESTAMPTZ,
    -- v10：SVIP 与配额
    svip_level          SMALLINT    NOT NULL DEFAULT 0,       -- 0=普通 1=SVIP
    svip_expires_at     TIMESTAMPTZ,                          -- NULL 且 level>0=永久
    mailbox_quota       INT         NOT NULL DEFAULT 0,       -- 0=默认, -1=无限, 正数=专属上限
    mailbox_ttl_minutes INT,                                   -- NULL=默认, 0=永久, 正数=专属 TTL
    -- v11：专属老粉认证
    exclusive_fan_level      SMALLINT   NOT NULL DEFAULT 0,    -- 0=未认证 1=专属老粉
    exclusive_fan_claimed_at TIMESTAMPTZ
);

-- API Key 查询走 B-tree 索引（认证热路径）
CREATE INDEX idx_accounts_api_key ON accounts (api_key);
CREATE INDEX idx_accounts_svip    ON accounts (svip_level) WHERE svip_level > 0;
CREATE INDEX idx_accounts_exclusive_fan ON accounts (exclusive_fan_level) WHERE exclusive_fan_level > 0;

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
INSERT INTO app_settings (key, value) VALUES ('announcement', '') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('announcement_title', '') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('announcement_level', 'info') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('site_title', 'TempMail') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('email_retention_days', '0') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('exclusive_fan_enabled', 'true') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('exclusive_fan_min_orders', '3') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('exclusive_fan_discount_bps', '9500') ON CONFLICT DO NOTHING;

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
    static_qr_enabled       BOOLEAN      NOT NULL DEFAULT TRUE,
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
INSERT INTO claude_shop_config (id) VALUES (1);

CREATE TABLE claude_shop_products (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sort_order            INT          NOT NULL DEFAULT 0,
    enabled               BOOLEAN      NOT NULL DEFAULT TRUE,
    title                 VARCHAR(160) NOT NULL,
    description           TEXT         NOT NULL DEFAULT '',
    tag                   VARCHAR(64)  NOT NULL DEFAULT '',
    retail_price_cents    INT          NOT NULL DEFAULT 0,
    wholesale_min_qty     INT          NOT NULL DEFAULT 5,
    wholesale_price_cents INT          NOT NULL DEFAULT 0,
    -- v10：自定义发货 + SVIP 专享价
    delivery_type         VARCHAR(16)  NOT NULL DEFAULT 'card_key',  -- card_key | text | custom_kv
    delivery_schema       JSONB        NOT NULL DEFAULT '{}'::jsonb, -- custom_kv 字段定义
    svip_price_cents      INT,                                       -- SVIP 专享价；NULL=未设置
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (wholesale_min_qty >= 1),
    CHECK (retail_price_cents >= 0),
    CHECK (wholesale_price_cents >= 0),
    CHECK (svip_price_cents IS NULL OR svip_price_cents >= 0),
    CHECK (delivery_type IN ('card_key','text','custom_kv'))
);
CREATE INDEX idx_claude_shop_products_enabled_sort ON claude_shop_products (enabled, sort_order, created_at);

CREATE TABLE claude_orders (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       UUID         NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    quantity         INT          NOT NULL CHECK (quantity >= 1 AND quantity <= 999),
    unit_price_cents INT          NOT NULL,
    total_cents      INT          NOT NULL,                    -- 实际应付（优惠后）
    is_wholesale     BOOLEAN      NOT NULL DEFAULT FALSE,
    status           VARCHAR(32)  NOT NULL DEFAULT 'awaiting_payment',
    payment_channel  VARCHAR(24)  NOT NULL DEFAULT 'static',
    alipay_trade_no  VARCHAR(64),
    product_id       UUID         REFERENCES claude_shop_products(id) ON DELETE SET NULL,
    product_title_snapshot VARCHAR(160) NOT NULL DEFAULT '',
    -- v10：优惠券 + 原价快照 + SVIP 快照
    original_total_cents INT         NOT NULL DEFAULT 0,       -- 优惠前金额（分）
    discount_cents       INT         NOT NULL DEFAULT 0,       -- 优惠金额（分）
    coupon_id            UUID,                                  -- 见下方外键
    coupon_code_snapshot VARCHAR(64) NOT NULL DEFAULT '',
    svip_snapshot        SMALLINT    NOT NULL DEFAULT 0,       -- 下单时 svip_level
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    fulfilled_at     TIMESTAMPTZ
);
CREATE INDEX idx_claude_orders_account ON claude_orders (account_id, created_at DESC);
CREATE INDEX idx_claude_orders_status ON claude_orders (status);

CREATE TABLE claude_inventory (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email        VARCHAR(320) NOT NULL DEFAULT '',               -- card_key 用；其他模式留空
    api_key      VARCHAR(128) NOT NULL DEFAULT '',
    status       VARCHAR(24)  NOT NULL DEFAULT 'available',
    order_id     UUID         REFERENCES claude_orders(id) ON DELETE SET NULL,
    batch_label  VARCHAR(64)  NOT NULL DEFAULT '',
    -- product_id 为 NULL 表示通用池；带 product_id 的订单优先从同 product_id 取货，
    -- 不足时兜底取通用池。见 migrate_v9.sql。
    product_id   UUID         REFERENCES claude_shop_products(id) ON DELETE SET NULL,
    -- v10/v12：自定义发货内容；v12 起库存也记录 delivery_type，通用池按类型兜底
    delivery_type VARCHAR(16)  NOT NULL DEFAULT 'card_key',
    payload       JSONB,                                          -- text: {"text":...}, custom_kv: {k:v,...}
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (delivery_type IN ('card_key','text','custom_kv'))
);
CREATE INDEX idx_claude_inv_available ON claude_inventory (created_at ASC) WHERE status = 'available';
CREATE INDEX idx_claude_inv_product_available ON claude_inventory (product_id, created_at ASC) WHERE status = 'available';
CREATE INDEX idx_claude_inv_delivery_available ON claude_inventory (delivery_type, product_id, created_at ASC) WHERE status = 'available';

CREATE TABLE claude_order_lines (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID        NOT NULL REFERENCES claude_orders(id) ON DELETE CASCADE,
    line_index    INT         NOT NULL,
    email         VARCHAR(320) NOT NULL DEFAULT '',
    api_key       VARCHAR(128) NOT NULL DEFAULT '',
    -- v10：发货内容快照
    payload       JSONB,
    delivery_type VARCHAR(16) NOT NULL DEFAULT 'card_key',
    UNIQUE (order_id, line_index)
);
CREATE INDEX idx_claude_order_lines_order ON claude_order_lines (order_id);

-- ============================================================
-- 10. 优惠券（v10）
-- ============================================================
CREATE TABLE coupons (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                 VARCHAR(64) UNIQUE,
    name                 VARCHAR(160) NOT NULL,
    description          TEXT         NOT NULL DEFAULT '',
    discount_type        VARCHAR(16)  NOT NULL,   -- percentage | fixed
    discount_value       INT          NOT NULL CHECK (discount_value >= 0),
    min_order_cents      INT          NOT NULL DEFAULT 0 CHECK (min_order_cents >= 0),
    max_discount_cents   INT          NOT NULL DEFAULT 0 CHECK (max_discount_cents >= 0),
    total_quota          INT          NOT NULL DEFAULT 0 CHECK (total_quota >= 0),
    used_count           INT          NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    per_user_limit       INT          NOT NULL DEFAULT 1 CHECK (per_user_limit >= 1),
    starts_at            TIMESTAMPTZ,
    expires_at           TIMESTAMPTZ,
    svip_only            BOOLEAN      NOT NULL DEFAULT FALSE,
    new_user_gift        BOOLEAN      NOT NULL DEFAULT FALSE,
    svip_gift            BOOLEAN      NOT NULL DEFAULT FALSE,
    fan_gift             BOOLEAN      NOT NULL DEFAULT FALSE,
    enabled              BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (discount_type IN ('percentage','fixed')),
    CHECK ((discount_type='percentage' AND discount_value<=100) OR discount_type='fixed')
);
CREATE INDEX idx_coupons_code      ON coupons (code)    WHERE code IS NOT NULL;
CREATE INDEX idx_coupons_enabled   ON coupons (enabled) WHERE enabled = TRUE;
CREATE INDEX idx_coupons_new_user  ON coupons (new_user_gift, enabled) WHERE new_user_gift = TRUE AND enabled = TRUE;
CREATE INDEX idx_coupons_svip_gift ON coupons (svip_gift,     enabled) WHERE svip_gift     = TRUE AND enabled = TRUE;
CREATE INDEX idx_coupons_fan_gift  ON coupons (fan_gift,      enabled) WHERE fan_gift      = TRUE AND enabled = TRUE;

-- 订单优惠券外键
ALTER TABLE claude_orders
    ADD CONSTRAINT claude_orders_coupon_fk
    FOREIGN KEY (coupon_id) REFERENCES coupons(id) ON DELETE SET NULL;

CREATE TABLE user_coupons (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    coupon_id   UUID        NOT NULL REFERENCES coupons(id)  ON DELETE CASCADE,
    status      VARCHAR(16) NOT NULL DEFAULT 'available',
    order_id    UUID        REFERENCES claude_orders(id)     ON DELETE SET NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at     TIMESTAMPTZ,
    snapshot_name               VARCHAR(160) NOT NULL DEFAULT '',
    snapshot_discount_type      VARCHAR(16)  NOT NULL DEFAULT '',
    snapshot_discount_value     INT          NOT NULL DEFAULT 0,
    snapshot_min_order_cents    INT          NOT NULL DEFAULT 0,
    snapshot_max_discount_cents INT          NOT NULL DEFAULT 0,
    snapshot_expires_at         TIMESTAMPTZ,
    CHECK (status IN ('available','used','expired','revoked'))
);
CREATE INDEX idx_user_coupons_account   ON user_coupons (account_id, status, acquired_at DESC);
CREATE INDEX idx_user_coupons_coupon    ON user_coupons (coupon_id, status);
CREATE INDEX idx_user_coupons_available ON user_coupons (account_id) WHERE status = 'available';

-- ============================================================
-- 11. SVIP activation codes (v12)
-- ============================================================
CREATE TABLE svip_activation_codes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code          VARCHAR(64)  NOT NULL UNIQUE,
    level         SMALLINT     NOT NULL DEFAULT 1,
    duration_days INT          NOT NULL DEFAULT 30, -- 0 = permanent SVIP
    max_uses      INT          NOT NULL DEFAULT 1,
    used_count    INT          NOT NULL DEFAULT 0,
    enabled       BOOLEAN      NOT NULL DEFAULT TRUE,
    note          VARCHAR(160) NOT NULL DEFAULT '',
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (level > 0),
    CHECK (duration_days >= 0),
    CHECK (max_uses > 0),
    CHECK (used_count >= 0 AND used_count <= max_uses)
);
CREATE INDEX idx_svip_activation_codes_created ON svip_activation_codes (created_at DESC);
CREATE INDEX idx_svip_activation_codes_enabled ON svip_activation_codes (enabled) WHERE enabled = TRUE;

CREATE TABLE svip_activation_redemptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code_id         UUID        NOT NULL REFERENCES svip_activation_codes(id) ON DELETE CASCADE,
    account_id      UUID        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    redeemed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    svip_expires_at TIMESTAMPTZ,
    UNIQUE (code_id, account_id)
);
CREATE INDEX idx_svip_activation_redemptions_account ON svip_activation_redemptions (account_id, redeemed_at DESC);

-- ============================================================
-- 12. API 调用统计（v13）
-- ============================================================
CREATE TABLE api_call_daily (
    account_id UUID        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    call_date  DATE        NOT NULL,
    call_count INT         NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, call_date)
);
CREATE INDEX idx_api_call_daily_date ON api_call_daily (call_date DESC);

-- ============================================================
-- 9. 数据库性能参数（在 postgresql.conf 或 docker 环境变量中设置更佳）
-- ============================================================
-- 以下通过 ALTER SYSTEM 设置，重启后生效
-- ALTER SYSTEM SET shared_buffers = '256MB';
-- ALTER SYSTEM SET effective_cache_size = '512MB';
-- ALTER SYSTEM SET work_mem = '4MB';
-- ALTER SYSTEM SET maintenance_work_mem = '64MB';
-- ALTER SYSTEM SET max_connections = 200;
