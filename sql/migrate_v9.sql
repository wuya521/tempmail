-- migrate_v9.sql — 商品独立卡券库存（混合池方案）
-- 执行前请确认已跑过 migrate_v8（含 claude_shop_products 多 SKU 表）
-- 老数据 product_id 保持 NULL，视为「通用池」；带 product_id 的订单取货时：
--   1) 先从同 product_id 的专属池 FIFO 扣库存
--   2) 不足时再从 product_id IS NULL 的通用池兜底补齐
-- 无 product_id 的老订单则仅从通用池取货，与历史行为兼容

ALTER TABLE claude_inventory
    ADD COLUMN IF NOT EXISTS product_id UUID
    REFERENCES claude_shop_products(id) ON DELETE SET NULL;

COMMENT ON COLUMN claude_inventory.product_id IS '卡券归属的 SKU；NULL 表示通用池，下单带 product_id 时专属池不足会回退到通用池兜底';

-- 分商品的待售索引（按 product_id 分组 + FIFO）；原 idx_claude_inv_available 仍保留，用于全局计数
CREATE INDEX IF NOT EXISTS idx_claude_inv_product_available
    ON claude_inventory (product_id, created_at ASC)
    WHERE status = 'available';
