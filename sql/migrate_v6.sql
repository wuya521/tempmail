-- 自助售号库存：批次标签（导入分组、按批筛选/删除）
ALTER TABLE claude_inventory ADD COLUMN IF NOT EXISTS batch_label VARCHAR(64) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_claude_inv_batch ON claude_inventory (batch_label);
