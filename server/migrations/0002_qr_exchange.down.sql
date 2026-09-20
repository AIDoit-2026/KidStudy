-- 回滚 0002：移除扫码兑换相关列。
ALTER TABLE qr_login_sessions
    DROP COLUMN IF EXISTS failed_count,
    DROP COLUMN IF EXISTS exchange_expires_at,
    DROP COLUMN IF EXISTS confirmed_at;
