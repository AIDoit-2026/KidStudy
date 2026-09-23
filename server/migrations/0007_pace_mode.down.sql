-- 0007 回退：pace_mode 收敛回 standard/fast 两档
--
-- 先把 review 归一到 standard，否则重新加回旧约束会失败（已有行违反 CHECK）。

UPDATE parent_settings SET pace_mode = 'standard', updated_at = now()
WHERE pace_mode = 'review';

ALTER TABLE parent_settings DROP CONSTRAINT IF EXISTS parent_settings_pace_valid;
ALTER TABLE parent_settings ADD CONSTRAINT parent_settings_pace_valid
    CHECK (pace_mode IN ('standard', 'fast'));

COMMENT ON COLUMN parent_settings.pace_mode IS NULL;
