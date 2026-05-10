-- v13：固定发货内容 + 弹窗配置

-- 商品表新增 fixed_content：非空时表示永久有货，发货时直接返回该内容
ALTER TABLE claude_shop_products
    ADD COLUMN IF NOT EXISTS fixed_content TEXT NOT NULL DEFAULT '';
