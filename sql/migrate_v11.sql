-- migrate_v11.sql — 专属老粉认证 / fan_gift 优惠券 / JSONB 参数修复配套
-- 执行前请确认已跑过 migrate_v10。

-- ============================================================
-- 一、accounts：专属老粉认证字段
-- ============================================================
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS exclusive_fan_level      SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS exclusive_fan_claimed_at TIMESTAMPTZ;

COMMENT ON COLUMN accounts.exclusive_fan_level      IS '0=未认证 1=专属老粉';
COMMENT ON COLUMN accounts.exclusive_fan_claimed_at IS '用户自行领取专属老粉认证的时间';

CREATE INDEX IF NOT EXISTS idx_accounts_exclusive_fan
    ON accounts (exclusive_fan_level)
    WHERE exclusive_fan_level > 0;

-- ============================================================
-- 二、coupons：领取专属老粉时自动赠送的优惠券模板
-- ============================================================
ALTER TABLE coupons
    ADD COLUMN IF NOT EXISTS fan_gift BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN coupons.fan_gift IS '用户首次领取专属老粉认证时自动赠送';

CREATE INDEX IF NOT EXISTS idx_coupons_fan_gift
    ON coupons (fan_gift, enabled)
    WHERE fan_gift = TRUE AND enabled = TRUE;

-- ============================================================
-- 三、app_settings：专属老粉规则
-- ============================================================
INSERT INTO app_settings (key, value) VALUES ('exclusive_fan_enabled', 'true') ON CONFLICT DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('exclusive_fan_min_orders', '3') ON CONFLICT DO NOTHING;
-- 商品折扣用基点表示实付比例：10000=10折，9500=9.5折，8800=8.8折。
INSERT INTO app_settings (key, value) VALUES ('exclusive_fan_discount_bps', '9500') ON CONFLICT DO NOTHING;
