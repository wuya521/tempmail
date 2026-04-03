-- v3 -> v4: 允许 mailboxes.expires_at 为 NULL，表示永不过期（mailbox_ttl_minutes = 0）
ALTER TABLE mailboxes ALTER COLUMN expires_at DROP NOT NULL;
