-- 0007 M7：节奏模式放开出「只复习」档
--
-- 背景：parent_settings.pace_mode 从 0001 起就存在，但从来没被读到过 ——
-- 编排（practice.Plan）只用了 daily_limit_min 与 subject_switches，
-- 而 /practice/start 甚至把 parentID 传成了 uuid.Nil，等于家长设置对孩子端会话完全无效。
-- M7 收口报表建议时把 pace_mode 接通（§4.7 规则 3/4 的「设为复习日」「上调每日量」），
-- 顺便修掉这个「设置了不生效」的问题。
--
-- review 档用于「复习积压时只复习、不学新」。约束只多一个取值，不动数据。

ALTER TABLE parent_settings DROP CONSTRAINT IF EXISTS parent_settings_pace_valid;
ALTER TABLE parent_settings ADD CONSTRAINT parent_settings_pace_valid
    CHECK (pace_mode IN ('standard', 'fast', 'review'));

COMMENT ON COLUMN parent_settings.pace_mode IS
    '节奏模式：standard 标准 / fast 加快新学 / review 只复习不学新（报表建议可一键切换）';
