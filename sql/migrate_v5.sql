-- v4 -> v5: 账户 last_seen、Claude 自助售号（库存、订单、店铺配置）

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS claude_shop_config (
    id                     SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    enabled                BOOLEAN      NOT NULL DEFAULT FALSE,
    title                  VARCHAR(160) NOT NULL DEFAULT 'Claude 成品账号',
    subtitle               VARCHAR(256) NOT NULL DEFAULT '',
    description            TEXT         NOT NULL DEFAULT '',
    tutorial_url           TEXT         NOT NULL DEFAULT '',
    retail_price_cents     INT          NOT NULL DEFAULT 9900,
    wholesale_min_qty      INT          NOT NULL DEFAULT 5,
    wholesale_price_cents INT         NOT NULL DEFAULT 7900,
    tag_hot                BOOLEAN      NOT NULL DEFAULT FALSE,
    show_tag_wholesale     BOOLEAN      NOT NULL DEFAULT TRUE,
    tag_fan_welfare        VARCHAR(64)  NOT NULL DEFAULT '',
    max_per_user           INT          NOT NULL DEFAULT 0,
    wechat_qr_file         VARCHAR(255) NOT NULL DEFAULT '',
    alipay_qr_file         VARCHAR(255) NOT NULL DEFAULT '',
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

INSERT INTO claude_shop_config (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS claude_orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     UUID         NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    quantity       INT          NOT NULL CHECK (quantity >= 1 AND quantity <= 999),
    unit_price_cents INT        NOT NULL,
    total_cents    INT          NOT NULL,
    is_wholesale   BOOLEAN      NOT NULL DEFAULT FALSE,
    status         VARCHAR(32)  NOT NULL DEFAULT 'awaiting_payment',
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    fulfilled_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_claude_orders_account ON claude_orders (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_claude_orders_status ON claude_orders (status);

CREATE TABLE IF NOT EXISTS claude_inventory (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email      VARCHAR(320) NOT NULL,
    api_key    VARCHAR(128) NOT NULL,
    status     VARCHAR(24)  NOT NULL DEFAULT 'available',
    order_id   UUID         REFERENCES claude_orders(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_claude_inv_available ON claude_inventory (created_at ASC)
    WHERE status = 'available';

CREATE TABLE IF NOT EXISTS claude_order_lines (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id   UUID        NOT NULL REFERENCES claude_orders(id) ON DELETE CASCADE,
    line_index INT         NOT NULL,
    email      VARCHAR(320) NOT NULL,
    api_key    VARCHAR(128) NOT NULL,
    UNIQUE (order_id, line_index)
);

CREATE INDEX IF NOT EXISTS idx_claude_order_lines_order ON claude_order_lines (order_id);
