-- M1 扫码登录：补充兑换码截止时间与失败计数。
-- qr_token 60 秒过期已在 0001；兑换码是确认后单独签发的，需要自己的 30 秒窗口。

ALTER TABLE qr_login_sessions
    ADD COLUMN IF NOT EXISTS confirmed_at        timestamptz,
    ADD COLUMN IF NOT EXISTS exchange_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS failed_count        int NOT NULL DEFAULT 0;

COMMENT ON COLUMN qr_login_sessions.exchange_expires_at IS '确认后签发的一次性兑换码截止时间（30 秒）';
COMMENT ON COLUMN qr_login_sessions.failed_count IS '兑换失败次数，累计 5 次置 failed 防暴力尝试';
