-- v6 -> v7: 支付宝当面付 precreate + 异步通知；静态收款可选是否需管理员确认

ALTER TABLE claude_shop_config
    ADD COLUMN IF NOT EXISTS static_payment_manual_confirm BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE claude_orders
    ADD COLUMN IF NOT EXISTS payment_channel VARCHAR(24) NOT NULL DEFAULT 'static';

ALTER TABLE claude_orders
    ADD COLUMN IF NOT EXISTS alipay_trade_no VARCHAR(64);

COMMENT ON COLUMN claude_orders.payment_channel IS 'static | alipay_precreate';
COMMENT ON COLUMN claude_shop_config.static_payment_manual_confirm IS '静态码订单是否需管理员确认发货；为 false 时下单后立即尝试自动发货';
