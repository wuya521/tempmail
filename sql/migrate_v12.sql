-- v12：库存发货类型隔离 + SVIP 激活码

ALTER TABLE claude_inventory
    ADD COLUMN IF NOT EXISTS delivery_type VARCHAR(16) NOT NULL DEFAULT 'card_key';

-- 兼容旧库存：有 SKU 归属的库存按 SKU 发货模式回填；无 SKU 的 payload 库存尽量按内容识别。
UPDATE claude_inventory ci
SET delivery_type = COALESCE(p.delivery_type, 'card_key')
FROM claude_shop_products p
WHERE ci.product_id = p.id
  AND (ci.payload IS NOT NULL OR COALESCE(p.delivery_type, 'card_key') = 'card_key');

UPDATE claude_inventory
SET delivery_type = 'text'
WHERE product_id IS NULL
  AND payload IS NOT NULL
  AND payload ? 'text';

UPDATE claude_inventory
SET delivery_type = 'custom_kv'
WHERE product_id IS NULL
  AND payload IS NOT NULL
  AND NOT (payload ? 'text');

DO $$
BEGIN
    ALTER TABLE claude_inventory
        ADD CONSTRAINT claude_inventory_delivery_type_check
        CHECK (delivery_type IN ('card_key','text','custom_kv'));
EXCEPTION WHEN duplicate_object THEN
    NULL;
END $$;

CREATE INDEX IF NOT EXISTS idx_claude_inv_delivery_available
    ON claude_inventory (delivery_type, product_id, created_at ASC)
    WHERE status = 'available';

CREATE TABLE IF NOT EXISTS svip_activation_codes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code          VARCHAR(64)  NOT NULL UNIQUE,
    level         SMALLINT     NOT NULL DEFAULT 1,
    duration_days INT          NOT NULL DEFAULT 30,
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

CREATE INDEX IF NOT EXISTS idx_svip_activation_codes_created
    ON svip_activation_codes (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_svip_activation_codes_enabled
    ON svip_activation_codes (enabled) WHERE enabled = TRUE;

CREATE TABLE IF NOT EXISTS svip_activation_redemptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code_id         UUID        NOT NULL REFERENCES svip_activation_codes(id) ON DELETE CASCADE,
    account_id      UUID        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    redeemed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    svip_expires_at TIMESTAMPTZ,
    UNIQUE (code_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_svip_activation_redemptions_account
    ON svip_activation_redemptions (account_id, redeemed_at DESC);
