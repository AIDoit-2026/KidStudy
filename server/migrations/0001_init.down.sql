-- 回退 0001_init：按依赖倒序删除。

DROP TABLE IF EXISTS parent_settings;
DROP TABLE IF EXISTS login_audit;
DROP TABLE IF EXISTS qr_login_sessions;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS children;
DROP TABLE IF EXISTS parents;
DROP INDEX IF EXISTS idx_stages_subject;
DROP TABLE IF EXISTS stages;
DROP TABLE IF EXISTS subjects;
