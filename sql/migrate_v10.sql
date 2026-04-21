-- migrate_v10.sql — SVIP 体系 / 优惠券 / 自定义发货 / 账户配额
-- 执行前请确认已跑过 migrate_v9。
--
-- 一、账户 SVIP / 配额 / TTL 覆盖
--     svip_level: 0=普通 1=SVIP（未来可扩展 SVIP+）
--     svip_expires_at: NULL 且 svip_level>0 表示永久；非 NULL 且过期视为已降级（应用层每次登录校验）
--     mailbox_quota: 0=使用全局默认(max_mailboxes_per_user)，-1=无限，正数=该账户专属上限
--     mailbox_ttl_minutes: NULL=使用全局默认；0=永不过期；正数=专属 TTL 分钟
-- 二、自定义发货
--     claude_shop_products.delivery_type: card_key(默认、兼容旧数据) | text | custom_kv
--     claude_shop_products.delivery_schema: custom_kv 下的字段定义 JSON {"fields":[{"key","label","hint"}]}
--     claude_shop_products.svip_price_cents: SVIP 专享价（分）；NULL 表示不设置 SVIP 折扣
--     claude_inventory.payload: text/custom_kv 模式下的发货内容；card_key 仍用 email+api_key 列
--     claude_order_lines.payload: 发货时从库存 payload 复制的快照（防库存改动影响历史订单）
-- 三、优惠券
--     coupons + user_coupons；claude_orders 增加优惠券/优惠金额快照

-- ============================================================
-- 一、accounts 扩展
-- ============================================================
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS svip_level          SMALLINT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS svip_expires_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS mailbox_quota       INT          NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS mailbox_ttl_minutes INT;

COMMENT ON COLUMN accounts.svip_level          IS '0=普通用户 1=SVIP（可扩展）';
COMMENT ON COLUMN accounts.svip_expires_at     IS 'NULL 表示永久（当 svip_level>0）；过期后应用层降级为 0';
COMMENT ON COLUMN accounts.mailbox_quota       IS '0=使用全局默认 -1=无限 正数=专属上限';
COMMENT ON COLUMN accounts.mailbox_ttl_minutes IS 'NULL=使用全局默认 0=永不过期 正数=专属 TTL';

CREATE INDEX IF NOT EXISTS idx_accounts_svip
    ON accounts (svip_level)
    WHERE svip_level > 0;

-- ============================================================
-- 二、claude_shop_products 扩展（自定义发货 + SVIP 专享价）
-- ============================================================
ALTER TABLE claude_shop_products
    ADD COLUMN IF NOT EXISTS delivery_type    VARCHAR(16) NOT NULL DEFAULT 'card_key',
    ADD COLUMN IF NOT EXISTS delivery_schema  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS svip_price_cents INT;

COMMENT ON COLUMN claude_shop_products.delivery_type    IS 'card_key(邮箱+Key) | text(长文本) | custom_kv(自定义多字段)';
COMMENT ON COLUMN claude_shop_products.delivery_schema  IS 'custom_kv 下的字段定义 {"fields":[{"key","label","hint","multiline"}]}';
COMMENT ON COLUMN claude_shop_products.svip_price_cents IS 'SVIP 专享价（分）；NULL=不设，沿用 retail_price_cents';

-- ============================================================
-- 三、claude_inventory 扩展（自定义发货 payload）
-- ============================================================
ALTER TABLE claude_inventory
    ADD COLUMN IF NOT EXISTS payload JSONB;

COMMENT ON COLUMN claude_inventory.payload IS 'text: {"text":"..."}；custom_kv: {key:value,...}；card_key 为空';

-- card_key 模式老数据的 email/api_key 列继续使用；新模式允许 email/api_key 为空字符串占位。
-- 放宽 api_key 列约束（仍 NOT NULL，但允许空串）
ALTER TABLE claude_inventory
    ALTER COLUMN email   SET DEFAULT '',
    ALTER COLUMN api_key SET DEFAULT '';

-- ============================================================
-- 四、claude_order_lines 扩展（发货快照 payload）
-- ============================================================
ALTER TABLE claude_order_lines
    ADD COLUMN IF NOT EXISTS payload       JSONB,
    ADD COLUMN IF NOT EXISTS delivery_type VARCHAR(16) NOT NULL DEFAULT 'card_key';

COMMENT ON COLUMN claude_order_lines.payload       IS '发货内容快照（text/custom_kv 模式）；card_key 仍用 email/api_key 列';
COMMENT ON COLUMN claude_order_lines.delivery_type IS '下单发货时记录的发货模式快照';

-- ============================================================
-- 五、claude_orders 扩展（优惠券 + 原价快照）
-- ============================================================
ALTER TABLE claude_orders
    ADD COLUMN IF NOT EXISTS original_total_cents INT          NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS discount_cents       INT          NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS coupon_id            UUID,
    ADD COLUMN IF NOT EXISTS coupon_code_snapshot VARCHAR(64)  NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS svip_snapshot        SMALLINT     NOT NULL DEFAULT 0;

COMMENT ON COLUMN claude_orders.original_total_cents IS '优惠前应付金额（分）；历史订单 0 表示与 total_cents 一致';
COMMENT ON COLUMN claude_orders.discount_cents       IS '本订单实际优惠金额（分）';
COMMENT ON COLUMN claude_orders.coupon_id            IS '引用 coupons(id)；允许 SET NULL';
COMMENT ON COLUMN claude_orders.coupon_code_snapshot IS '下单时优惠券 code 快照，便于审计';
COMMENT ON COLUMN claude_orders.svip_snapshot        IS '下单时该账户的 svip_level 快照';

-- ============================================================
-- 六、优惠券定义表
-- ============================================================
CREATE TABLE IF NOT EXISTS coupons (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                 VARCHAR(64) UNIQUE,                          -- NULL = 仅定向派发，不对外领取
    name                 VARCHAR(160) NOT NULL,
    description          TEXT         NOT NULL DEFAULT '',
    discount_type        VARCHAR(16)  NOT NULL,                       -- percentage(0-100整数) | fixed(分)
    discount_value       INT          NOT NULL CHECK (discount_value >= 0),
    min_order_cents      INT          NOT NULL DEFAULT 0 CHECK (min_order_cents >= 0),
    max_discount_cents   INT          NOT NULL DEFAULT 0 CHECK (max_discount_cents >= 0),   -- percentage 封顶；0=无上限
    total_quota          INT          NOT NULL DEFAULT 0 CHECK (total_quota >= 0),          -- 0=无上限
    used_count           INT          NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    per_user_limit       INT          NOT NULL DEFAULT 1 CHECK (per_user_limit >= 1),       -- 同账户最多领几张
    starts_at            TIMESTAMPTZ,
    expires_at           TIMESTAMPTZ,
    svip_only            BOOLEAN      NOT NULL DEFAULT FALSE,                               -- 仅 SVIP 可领/用
    new_user_gift        BOOLEAN      NOT NULL DEFAULT FALSE,                               -- 注册时自动赠送
    svip_gift            BOOLEAN      NOT NULL DEFAULT FALSE,                               -- 授予 SVIP 时自动赠送
    enabled              BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CHECK (discount_type IN ('percentage','fixed')),
    CHECK (
        (discount_type = 'percentage' AND discount_value <= 100)
        OR discount_type = 'fixed'
    )
);

CREATE INDEX IF NOT EXISTS idx_coupons_code      ON coupons (code)    WHERE code IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_coupons_enabled   ON coupons (enabled) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_coupons_new_user  ON coupons (new_user_gift, enabled) WHERE new_user_gift = TRUE AND enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_coupons_svip_gift ON coupons (svip_gift,     enabled) WHERE svip_gift     = TRUE AND enabled = TRUE;

COMMENT ON COLUMN coupons.code           IS '公开领取码，用户可 POST /api/coupons/redeem {code} 领取；NULL 时仅支持定向派发';
COMMENT ON COLUMN coupons.per_user_limit IS '单用户可重复领取的次数（领取后进入 user_coupons，每张独立使用）';
COMMENT ON COLUMN coupons.new_user_gift  IS '新账户首次登录 /api/me 时自动发一张';
COMMENT ON COLUMN coupons.svip_gift      IS '管理员授予 SVIP 时自动发一张';

-- ============================================================
-- 七、用户-优惠券关联表（每次领取生成一条）
-- ============================================================
CREATE TABLE IF NOT EXISTS user_coupons (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    coupon_id   UUID        NOT NULL REFERENCES coupons(id)  ON DELETE CASCADE,
    status      VARCHAR(16) NOT NULL DEFAULT 'available',  -- available | used | expired | revoked
    order_id    UUID        REFERENCES claude_orders(id)    ON DELETE SET NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at     TIMESTAMPTZ,
    -- 冗余快照，便于用户列表展示优惠券即使被管理员删除也能看见
    snapshot_name            VARCHAR(160) NOT NULL DEFAULT '',
    snapshot_discount_type   VARCHAR(16)  NOT NULL DEFAULT '',
    snapshot_discount_value  INT          NOT NULL DEFAULT 0,
    snapshot_min_order_cents INT          NOT NULL DEFAULT 0,
    snapshot_max_discount_cents INT       NOT NULL DEFAULT 0,
    snapshot_expires_at      TIMESTAMPTZ,
    CHECK (status IN ('available','used','expired','revoked'))
);

CREATE INDEX IF NOT EXISTS idx_user_coupons_account   ON user_coupons (account_id, status, acquired_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_coupons_coupon    ON user_coupons (coupon_id, status);
CREATE INDEX IF NOT EXISTS idx_user_coupons_available ON user_coupons (account_id) WHERE status = 'available';

COMMENT ON TABLE user_coupons IS '用户领取的优惠券实例；同一账户同一模板可有多张（受 per_user_limit 限制）';

-- ============================================================
-- 八、建立外键（orders.coupon_id → coupons.id，历史数据兼容）
-- ============================================================
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'claude_orders_coupon_fk'
    ) THEN
        ALTER TABLE claude_orders
            ADD CONSTRAINT claude_orders_coupon_fk
            FOREIGN KEY (coupon_id) REFERENCES coupons(id) ON DELETE SET NULL;
    END IF;
END $$;

-- ============================================================
-- 九、历史订单原价快照修复：original_total_cents 若为 0 则补成 total_cents + discount_cents
--     （新订单由应用层写入，这里仅一次性兜底）
-- ============================================================
UPDATE claude_orders
SET original_total_cents = total_cents
WHERE original_total_cents = 0;

-- ============================================================
-- 十、app_settings: 公告允许 Markdown / 多段文本，新增 announcement_level 与 announcement_title
--     为 v5 美化方案 A 预留（单条公告，但支持标题 + 级别 + Markdown 正文）
-- ============================================================
INSERT INTO app_settings (key, value) VALUES ('announcement_title', '')     ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('announcement_level', 'info') ON CONFLICT DO NOTHING;  -- info | success | warn | danger
