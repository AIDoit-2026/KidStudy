-- 0004 学习域：掌握度、会话、答题流水、错题本、日汇总、专项指派
--
-- 设计依据：《开发设计文档》§3.3 学习域、§4.2 今日编排、§4.3 判分、§4.4 间隔重复。
--
-- 两处对设计的偏离（都是有意为之，写在这里免得后人当成 bug）：
--  1. interval_hours 用 numeric(10,4) 而不是 int —— §4.4 里答错要写 0.17（10 分钟），
--     整型存不下，取整会让「10 分钟后复现」变成 0 小时（立即复现）。
--  2. session_items 拆出 answer_key 列与 question_snapshot 并列 —— 题面快照要能完整复现
--     报表，但答案绝不能跟着题面一起下发；放在同一列里只能靠序列化时手挑字段，容易漏。

CREATE TABLE mastery_records (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    child_id          uuid           NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    kp_id             uuid           NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    level             smallint       NOT NULL DEFAULT 0,
    ease              numeric(3, 2)  NOT NULL DEFAULT 2.50,
    interval_hours    numeric(10, 4) NOT NULL DEFAULT 0,
    next_review_at    timestamptz    NOT NULL DEFAULT now(),
    difficulty        smallint       NOT NULL DEFAULT 1,
    correct_count     int            NOT NULL DEFAULT 0,
    wrong_count       int            NOT NULL DEFAULT 0,
    streak            int            NOT NULL DEFAULT 0,
    wrong_streak      int            NOT NULL DEFAULT 0,
    last_result       text,
    first_learned_at  timestamptz,
    mastered_at       timestamptz,
    attempts          int            NOT NULL DEFAULT 0,
    attempts_to_master int,
    repeat_days       int            NOT NULL DEFAULT 0,
    last_review_date  date,
    created_at        timestamptz    NOT NULL DEFAULT now(),
    updated_at        timestamptz    NOT NULL DEFAULT now(),
    CONSTRAINT mastery_unique          UNIQUE (child_id, kp_id),
    CONSTRAINT mastery_level_valid     CHECK (level BETWEEN 0 AND 5),
    CONSTRAINT mastery_ease_valid      CHECK (ease BETWEEN 1.3 AND 3.0),
    CONSTRAINT mastery_difficulty_valid CHECK (difficulty BETWEEN 1 AND 5),
    CONSTRAINT mastery_result_valid    CHECK (last_result IS NULL OR last_result IN ('correct', 'wrong', 'skipped'))
);

COMMENT ON TABLE mastery_records IS '掌握度唯一挂载点：每个（孩子, 知识点）一条，简化 SM-2 的状态全在这里';
COMMENT ON COLUMN mastery_records.level IS '0 未学 / 1-2 学习中 / 3 已掌握 / 4-5 巩固与维持';
COMMENT ON COLUMN mastery_records.interval_hours IS '下次复习间隔（小时）。0.17 表示 10 分钟后会话内复现';
COMMENT ON COLUMN mastery_records.difficulty IS '难度档，由难度自适应升降（§4.2：连 3 次正确率<60% 降档）';
COMMENT ON COLUMN mastery_records.attempts_to_master IS '掌握前花了多少次作答；与 attempts 相除即学习效率';
COMMENT ON COLUMN mastery_records.repeat_days IS '出现在多少个不同学习日，用于统计「标准内容的重复次数」';

CREATE INDEX IF NOT EXISTS idx_mastery_due ON mastery_records (child_id, next_review_at) WHERE level > 0;
CREATE INDEX IF NOT EXISTS idx_mastery_new ON mastery_records (child_id, level) WHERE level = 0;

CREATE TABLE learning_sessions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    child_id       uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    device_type    text        NOT NULL DEFAULT 'unknown',
    started_at     timestamptz NOT NULL DEFAULT now(),
    ended_at       timestamptz,
    duration_sec   int         NOT NULL DEFAULT 0,
    question_count int         NOT NULL DEFAULT 0,
    answered_count int         NOT NULL DEFAULT 0,
    correct_count  int         NOT NULL DEFAULT 0,
    skipped_count  int         NOT NULL DEFAULT 0,
    star_count     int         NOT NULL DEFAULT 0,
    completed_by   text,
    parent_score   smallint,
    parent_note    text,
    status         text        NOT NULL DEFAULT 'active',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT session_status_valid    CHECK (status IN ('active', 'finished', 'expired')),
    CONSTRAINT session_completed_valid CHECK (completed_by IS NULL OR completed_by IN ('auto', 'parent', 'mixed')),
    CONSTRAINT session_score_valid     CHECK (parent_score IS NULL OR parent_score BETWEEN 1 AND 5)
);

COMMENT ON TABLE learning_sessions IS '一次学习 = 一个会话；家长可补录主观评价（parent_score），不计入客观正确率';
COMMENT ON COLUMN learning_sessions.completed_by IS 'auto 程序判定 / parent 家长确认或补录 / mixed 两者皆有';

CREATE INDEX IF NOT EXISTS idx_sessions_child ON learning_sessions (child_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_active ON learning_sessions (child_id) WHERE status = 'active';

CREATE TABLE session_items (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id        uuid        NOT NULL REFERENCES learning_sessions(id) ON DELETE CASCADE,
    seq               int         NOT NULL,
    kp_id             uuid        NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    subject_code      text        NOT NULL,
    stage_code        text,
    question_type     text        NOT NULL,
    difficulty        smallint    NOT NULL DEFAULT 1,
    question_snapshot jsonb       NOT NULL,
    answer_key        jsonb       NOT NULL,
    state             text        NOT NULL DEFAULT 'pending',
    is_correct        boolean,
    used_hint         boolean     NOT NULL DEFAULT false,
    elapsed_ms        int,
    answered_at       timestamptz,
    CONSTRAINT item_unique       UNIQUE (session_id, seq),
    CONSTRAINT item_state_valid  CHECK (state IN ('pending', 'answered', 'skipped')),
    CONSTRAINT item_type_valid   CHECK (question_type IN (
        'choice_text', 'choice_image', 'choice_audio', 'fill_blank',
        'match', 'order', 'trace', 'say', 'math_param'))
);

COMMENT ON TABLE session_items IS '题目快照：答题即落库，报表可复现，不依赖题库后续变更';
COMMENT ON COLUMN session_items.question_snapshot IS '题面（prompt/options/media），可直接下发给前端';
COMMENT ON COLUMN session_items.answer_key IS '正确答案，服务端判分专用，任何读接口都不返回';
COMMENT ON COLUMN session_items.is_correct IS 'NULL = 未判定（trace/say 等主观项），不计入客观正确率';

CREATE INDEX IF NOT EXISTS idx_items_session ON session_items (session_id, seq);

CREATE TABLE answer_logs (
    id           bigserial PRIMARY KEY,
    child_id     uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    session_id   uuid        REFERENCES learning_sessions(id) ON DELETE SET NULL,
    item_id      uuid        REFERENCES session_items(id) ON DELETE SET NULL,
    kp_id        uuid        NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    subject_code text        NOT NULL,
    question_type text       NOT NULL DEFAULT '',
    is_correct   boolean     NOT NULL,
    used_hint    boolean     NOT NULL DEFAULT false,
    elapsed_ms   int         NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE answer_logs IS '原始答题流水，日汇总的原料。按决策 #6 不分区';

CREATE INDEX IF NOT EXISTS idx_answer_child_time ON answer_logs (child_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_answer_kp ON answer_logs (child_id, kp_id, created_at DESC);

CREATE TABLE wrong_book_entries (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    child_id            uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    kp_id               uuid        NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    added_at            timestamptz NOT NULL DEFAULT now(),
    cleared_at          timestamptz,
    consecutive_correct int         NOT NULL DEFAULT 0,
    wrong_count         int         NOT NULL DEFAULT 1,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT wrongbook_unique UNIQUE (child_id, kp_id)
);

COMMENT ON TABLE wrong_book_entries IS '错题本：同一 kp 只保留一条，答错重新打开（cleared_at 置空），连对 3 次自动移出';
COMMENT ON COLUMN wrong_book_entries.cleared_at IS 'NULL = 在错题中；非 NULL = 已移出（保留记录供统计）';

CREATE INDEX IF NOT EXISTS idx_wrongbook_open ON wrong_book_entries (child_id, added_at DESC) WHERE cleared_at IS NULL;

CREATE TABLE daily_stats (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    child_id       uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    stat_date      date        NOT NULL,
    subject_code   text        NOT NULL,
    duration_sec   int         NOT NULL DEFAULT 0,
    question_count int         NOT NULL DEFAULT 0,
    correct_count  int         NOT NULL DEFAULT 0,
    new_mastered   int         NOT NULL DEFAULT 0,
    star_count     int         NOT NULL DEFAULT 0,
    planned_new    int         NOT NULL DEFAULT 0,
    actual_new     int         NOT NULL DEFAULT 0,
    repeat_count   int         NOT NULL DEFAULT 0,
    cum_planned    int         NOT NULL DEFAULT 0,
    cum_actual     int         NOT NULL DEFAULT 0,
    deviation_days numeric(8, 2) NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT daily_unique UNIQUE (child_id, stat_date, subject_code)
);

COMMENT ON TABLE daily_stats IS '日汇总：会话结束时同步增量更新（量小）；planned/cum/deviation 三列留待 M4 报表与节奏偏差';
COMMENT ON COLUMN daily_stats.deviation_days IS '正 = 落后标准节奏 N 天，负 = 提前';

CREATE TABLE practice_assignments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    child_id   uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    kp_id      uuid        NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    parent_id  uuid        NOT NULL REFERENCES parents(id) ON DELETE CASCADE,
    reason     text        NOT NULL DEFAULT '',
    status     text        NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz,
    CONSTRAINT assign_unique  UNIQUE (child_id, kp_id),
    CONSTRAINT assign_status_valid CHECK (status IN ('pending', 'done'))
);

COMMENT ON TABLE practice_assignments IS '家长专项指派：来自报表建议或打印补录，优先进入今日任务（§4.2 步骤 5）';

CREATE INDEX IF NOT EXISTS idx_assign_pending ON practice_assignments (child_id, status, created_at);

-- 数学模板种子：题库无限靠参数化生成，这里只落模板定义 + 对应知识点。
-- 用 ON CONFLICT 保证重跑迁移不会重复插入。
INSERT INTO math_templates (code, question_type, difficulty_band, generator_config, display_config, status) VALUES
    ('M1_ADD5',     'math_param', 'M1', '{"op":"add","min":0,"max":5,"terms":2}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M1_SUB5',     'math_param', 'M1', '{"op":"sub","min":0,"max":5,"terms":2,"no_negative":true}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M2_ADD10',    'math_param', 'M2', '{"op":"add","min":0,"max":10,"terms":2}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M2_SUB10',    'math_param', 'M2', '{"op":"sub","min":0,"max":10,"terms":2,"no_negative":true}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M3_ADD20',    'math_param', 'M3', '{"op":"add","min":0,"max":20,"terms":2}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M3_SUB20',    'math_param', 'M3', '{"op":"sub","min":0,"max":20,"terms":2,"no_negative":true}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M3_CMP20',    'math_param', 'M3', '{"op":"cmp","min":0,"max":20,"terms":2}'::jsonb,
                    '{"layout":"compare"}'::jsonb, 'published'),
    ('M4_ADD100',   'math_param', 'M4', '{"op":"add","min":10,"max":100,"terms":2}'::jsonb,
                    '{"layout":"vertical"}'::jsonb, 'published'),
    ('M4_SUB100',   'math_param', 'M4', '{"op":"sub","min":10,"max":100,"terms":2,"no_negative":true}'::jsonb,
                    '{"layout":"vertical"}'::jsonb, 'published'),
    ('M4_MULTABLE', 'math_param', 'M4', '{"op":"mul","min":2,"max":9,"terms":2}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M4_DIVTABLE', 'math_param', 'M4', '{"op":"div","min":2,"max":9,"terms":2,"exact":true}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M5_MUL2X1',   'math_param', 'M5', '{"op":"mul","min":11,"max":99,"terms":2,"second_max":9}'::jsonb,
                    '{"layout":"vertical"}'::jsonb, 'published'),
    ('M5_MIXED',    'math_param', 'M5', '{"op":"mixed","min":2,"max":20,"terms":3}'::jsonb,
                    '{"layout":"horizontal"}'::jsonb, 'published'),
    ('M5_WORD',     'math_param', 'M5', '{"op":"word","min":1,"max":20,"terms":2}'::jsonb,
                    '{"layout":"text"}'::jsonb, 'published')
ON CONFLICT (code) DO UPDATE SET
    difficulty_band  = EXCLUDED.difficulty_band,
    generator_config = EXCLUDED.generator_config,
    display_config   = EXCLUDED.display_config,
    status           = 'published';

-- 为每个数学模板挂一个知识点（掌握度只认 knowledge_points）
INSERT INTO knowledge_points (subject_code, stage_code, kind, ref_id, code, name, difficulty, metadata, status)
SELECT 'math',
       t.difficulty_band,
       'math_skill',
       t.id,
       'math:' || t.code,
       v.name,
       substring(t.difficulty_band FROM 2)::smallint,
       jsonb_build_object('template_code', t.code),
       'published'
FROM math_templates t
JOIN (VALUES
    ('M1_ADD5',     '5 以内加法'),
    ('M1_SUB5',     '5 以内减法'),
    ('M2_ADD10',    '10 以内加法'),
    ('M2_SUB10',    '10 以内减法'),
    ('M3_ADD20',    '20 以内加法'),
    ('M3_SUB20',    '20 以内减法'),
    ('M3_CMP20',    '20 以内比大小'),
    ('M4_ADD100',   '100 以内加法'),
    ('M4_SUB100',   '100 以内减法'),
    ('M4_MULTABLE', '表内乘法'),
    ('M4_DIVTABLE', '表内除法'),
    ('M5_MUL2X1',   '两位数乘一位数'),
    ('M5_MIXED',    '两步混合运算'),
    ('M5_WORD',     '简单应用题')
) AS v(code, name) ON v.code = t.code
ON CONFLICT (code) DO UPDATE SET
    ref_id      = EXCLUDED.ref_id,
    stage_code  = EXCLUDED.stage_code,
    name        = EXCLUDED.name,
    difficulty  = EXCLUDED.difficulty,
    metadata    = EXCLUDED.metadata,
    status      = 'published';
