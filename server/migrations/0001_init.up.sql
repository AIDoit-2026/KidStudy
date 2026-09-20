-- 初始 schema：字典表（学科/阶段）+ 身份域（家长/孩子/令牌/扫码会话/审计/家长设置）。
-- 注意：所有时间为 timestamptz（UTC），软状态用文本 + 检查约束。

CREATE TABLE IF NOT EXISTS subjects (
    code        text PRIMARY KEY,
    name        text        NOT NULL,
    sort_order  int         NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE subjects IS '学科字典：chinese / math / english';

INSERT INTO subjects (code, name, sort_order) VALUES
    ('chinese', '语文', 1),
    ('math',    '数学', 2),
    ('english', '英语', 3)
ON CONFLICT (code) DO NOTHING;

CREATE TABLE IF NOT EXISTS stages (
    code         text PRIMARY KEY,
    subject_code text        NOT NULL REFERENCES subjects(code) ON DELETE RESTRICT,
    name         text        NOT NULL,
    sort_order   int         NOT NULL DEFAULT 0,
    target_count int         NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE stages IS '学习阶段基准：S0-S5 / G1-G12 / X1-X6（语文），E1-E10（英语），L1-L5（数学）';

CREATE INDEX IF NOT EXISTS idx_stages_subject ON stages (subject_code, sort_order);

CREATE TABLE IF NOT EXISTS parents (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        UNIQUE,
    phone         text        UNIQUE,
    password_hash text        NOT NULL,
    pin_hash      text,
    display_name  text        NOT NULL,
    status        text        NOT NULL DEFAULT 'active',
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT parents_contact_required CHECK (email IS NOT NULL OR phone IS NOT NULL),
    CONSTRAINT parents_status_valid CHECK (status IN ('active', 'disabled'))
);

COMMENT ON TABLE parents IS '家长账号：邮箱与手机号至少其一；密码 bcrypt，PIN 用于二次校验';

CREATE TABLE IF NOT EXISTS children (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id  uuid        NOT NULL REFERENCES parents(id) ON DELETE CASCADE,
    nickname   text        NOT NULL,
    avatar_id  text        NOT NULL DEFAULT 'panda',
    birth_ym   char(6),
    stage_code text        REFERENCES stages(code) ON DELETE SET NULL,
    active     boolean     NOT NULL DEFAULT true,
    settings   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE children IS '孩子档案：一个家长独占自己的孩子，不跨家长共享';
COMMENT ON COLUMN children.birth_ym IS '只保留年月（YYYYMM），符合儿童隐私最小化要求';

CREATE INDEX IF NOT EXISTS idx_children_parent ON children (parent_id) WHERE active;

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id   uuid        NOT NULL REFERENCES parents(id) ON DELETE CASCADE,
    token_hash  text        NOT NULL UNIQUE,
    user_agent  text,
    ip          inet,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    replaced_by uuid        REFERENCES refresh_tokens(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE refresh_tokens IS '刷新令牌：30 天有效，轮换时由 replaced_by 指认后继，支持服务端撤销';

CREATE INDEX IF NOT EXISTS idx_refresh_parent ON refresh_tokens (parent_id, revoked_at);
CREATE INDEX IF NOT EXISTS idx_refresh_expires ON refresh_tokens (expires_at);

CREATE TABLE IF NOT EXISTS qr_login_sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    qr_token           text        NOT NULL UNIQUE,
    status             text        NOT NULL DEFAULT 'waiting',
    creator_ip         inet,
    creator_ua         text,
    parent_id          uuid        REFERENCES parents(id) ON DELETE SET NULL,
    scan_count         int         NOT NULL DEFAULT 0,
    exchange_code_hash text,
    expires_at         timestamptz NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT qr_status_valid CHECK (status IN ('waiting', 'scanned', 'confirmed', 'exchanged', 'expired', 'failed'))
);

COMMENT ON TABLE qr_login_sessions IS '扫码登录会话：二维码 60 秒过期，兑换码一次性';

CREATE INDEX IF NOT EXISTS idx_qr_expires ON qr_login_sessions (expires_at);

CREATE TABLE IF NOT EXISTS login_audit (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id  uuid        REFERENCES parents(id) ON DELETE SET NULL,
    event      text        NOT NULL,
    ip         inet,
    ua         text,
    success    boolean     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE login_audit IS '登录审计：记录登录/扫码/兑换等事件，便于排查异常登录';

CREATE INDEX IF NOT EXISTS idx_login_audit_parent ON login_audit (parent_id, created_at DESC);

CREATE TABLE IF NOT EXISTS parent_settings (
    parent_id             uuid PRIMARY KEY REFERENCES parents(id) ON DELETE CASCADE,
    daily_limit_min       int         NOT NULL DEFAULT 60,
    session_limit_min     int         NOT NULL DEFAULT 20,
    allowed_window        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    rest_interval_min     int         NOT NULL DEFAULT 20,
    subject_switches      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    theme                 text        NOT NULL DEFAULT 'paper',
    font_scale            text        NOT NULL DEFAULT 'standard',
    low_blue_light        boolean     NOT NULL DEFAULT true,
    require_parent_confirm boolean    NOT NULL DEFAULT false,
    pace_mode             text        NOT NULL DEFAULT 'standard',
    compare_children      boolean     NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT parent_settings_theme_valid CHECK (theme IN ('paper', 'dark', 'system')),
    CONSTRAINT parent_settings_font_valid CHECK (font_scale IN ('small', 'standard', 'large')),
    CONSTRAINT parent_settings_pace_valid CHECK (pace_mode IN ('standard', 'fast'))
);

COMMENT ON TABLE parent_settings IS '家长控制：时长、休息间隔、学科开关、护眼主题与节奏基准模式';
