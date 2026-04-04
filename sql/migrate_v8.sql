-- 静态收款码总开关；多商品 SKU；订单关联商品快照
-- 执行前请确认已跑过 migrate_v7

ALTER TABLE claude_shop_config
    ADD COLUMN IF NOT EXISTS static_qr_enabled BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN claude_shop_config.static_qr_enabled IS '关闭时：不展示静态码；不限制当面付多笔待支付。开启时：可走静态码，且静态路径下一账号仅允许一笔 awaiting_payment(static)；当面付不受此限';

CREATE TABLE IF NOT EXISTS claude_shop_products (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sort_order            INT          NOT NULL DEFAULT 0,
    enabled               BOOLEAN      NOT NULL DEFAULT TRUE,
    title                 VARCHAR(160) NOT NULL,
    description           TEXT         NOT NULL DEFAULT '',
    tag                   VARCHAR(64)  NOT NULL DEFAULT '',
    retail_price_cents    INT          NOT NULL DEFAULT 0,
    wholesale_min_qty     INT          NOT NULL DEFAULT 5,
    wholesale_price_cents INT          NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (wholesale_min_qty >= 1),
    CHECK (retail_price_cents >= 0),
    CHECK (wholesale_price_cents >= 0)
);
CREATE INDEX IF NOT EXISTS idx_claude_shop_products_enabled_sort ON claude_shop_products (enabled, sort_order, created_at);

ALTER TABLE claude_orders
    ADD COLUMN IF NOT EXISTS product_id UUID REFERENCES claude_shop_products(id) ON DELETE SET NULL;
ALTER TABLE claude_orders
    ADD COLUMN IF NOT EXISTS product_title_snapshot VARCHAR(160) NOT NULL DEFAULT '';

COMMENT ON COLUMN claude_orders.product_title_snapshot IS '下单时商品标题快照，便于列表展示与历史追溯';
