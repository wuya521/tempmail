-- v13：API 调用统计 + 邮件自动清理设置

CREATE TABLE IF NOT EXISTS api_call_daily (
    account_id UUID        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    call_date  DATE        NOT NULL,
    call_count INT         NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, call_date)
);
CREATE INDEX IF NOT EXISTS idx_api_call_daily_date ON api_call_daily (call_date DESC);

INSERT INTO app_settings (key, value) VALUES ('email_retention_days', '0') ON CONFLICT DO NOTHING;
