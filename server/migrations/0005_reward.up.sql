-- 0005 评价域：成就徽章 + 日汇总的达标标记
--
-- 设计依据：《开发设计文档》§3.3（badges / child_badges）、§4.7 报表、§4.9 节奏偏差、§4.11 完成判定。
--
-- 一处对设计的补充（有意为之）：
--  daily_stats 增加 passed / parent_confirmed 两列 —— 「当日徽章是否点亮」需要一个落库的
--  判定结果，否则每次查询都要重放一遍规则、且无法表达「家长确认后补点亮」。
--  约定：subject_code = '' 的那一行是「当天全天合计」，只有这一行的 passed 代表当日达标；
--  分学科行的 passed 表示该学科当日是否达标（M3 起就在写 '' 行，这里把这个约定固化下来）。

CREATE TABLE badges (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code        text        NOT NULL UNIQUE,
    name        text        NOT NULL,
    category    text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    icon        text        NOT NULL DEFAULT '',
    rule        jsonb       NOT NULL,
    sort_order  int         NOT NULL DEFAULT 0,
    active      boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT badge_category_valid CHECK (category IN ('session', 'streak', 'mastery', 'star')),
    CONSTRAINT badge_rule_object     CHECK (jsonb_typeof(rule) = 'object')
);

COMMENT ON TABLE badges IS '成就徽章定义：rule 是 JSON 规则（type + threshold + 可选 subject），由 report 模块的评测器解释';
COMMENT ON COLUMN badges.rule IS '判定规则，如 {"type":"mastery_count","subject":"chinese","threshold":10}';
COMMENT ON COLUMN badges.category IS 'session=单次会话 / streak=连续达标 / mastery=掌握量 / star=星星累计';

CREATE INDEX idx_badges_active ON badges (active, sort_order);

CREATE TABLE child_badges (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    child_id   uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    badge_id   uuid        NOT NULL REFERENCES badges(id) ON DELETE CASCADE,
    earned_at  timestamptz NOT NULL DEFAULT now(),
    progress   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT child_badge_unique UNIQUE (child_id, badge_id)
);

COMMENT ON TABLE child_badges IS '孩子已获得的成就：unique(child_id, badge_id) 保证同一致就只落一条，授予天然幂等';
COMMENT ON COLUMN child_badges.progress IS '授予时的事实快照（当时的掌握量 / 连续天数等），供家长查看「凭什么拿到的」';

CREATE INDEX idx_child_badges_child ON child_badges (child_id, earned_at DESC);

-- 日汇总的达标标记（见文件头说明）
ALTER TABLE daily_stats
    ADD COLUMN IF NOT EXISTS passed           boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS parent_confirmed  boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN daily_stats.passed IS '当日是否达标（全程合计行 subject_code='''' 才代表当日；要求客观题完成且正确率达标）';
COMMENT ON COLUMN daily_stats.parent_confirmed IS '当日是否有家长确认记录；require_parent_confirm 开启时，达标必须由它点亮';

-- 成就种子：rule 全部可解释，家长端能直译成「掌握 10 个汉字」这类文案。
-- ON CONFLICT 保证重跑迁移幂等，定义改了也只会更新。
INSERT INTO badges (code, name, category, description, icon, rule, sort_order) VALUES
    ('first_session',  '第一次练习',   'session', '完成第一次练习',           'spark',  '{"type":"session_count","threshold":1}'::jsonb,                                      10),
    ('perfect_1',      '满分小达人',   'session', '一次会话全对',             'star',   '{"type":"perfect_sessions","threshold":1}'::jsonb,                                  20),
    ('session_20',     '坚持练习',     'session', '累计完成 20 次练习',       'medal',  '{"type":"session_count","threshold":20}'::jsonb,                                    30),
    ('streak_3',       '连续三天',     'streak',  '连续 3 天完成学习',        'flame',  '{"type":"streak_days","threshold":3}'::jsonb,                                      40),
    ('streak_7',       '一周不断',     'streak',  '连续 7 天完成学习',        'flame',  '{"type":"streak_days","threshold":7}'::jsonb,                                      50),
    ('streak_30',      '月度坚持',     'streak',  '连续 30 天完成学习',       'crown',  '{"type":"streak_days","threshold":30}'::jsonb,                                     60),
    ('cn_10',          '识字起步',     'mastery', '掌握 10 个汉字',           'book',   '{"type":"mastery_count","subject":"chinese","threshold":10}'::jsonb,                70),
    ('cn_50',          '识字小能手',   'mastery', '掌握 50 个汉字',           'book',   '{"type":"mastery_count","subject":"chinese","threshold":50}'::jsonb,                80),
    ('cn_100',         '识字达人',     'mastery', '掌握 100 个汉字',          'book',   '{"type":"mastery_count","subject":"chinese","threshold":100}'::jsonb,               90),
    ('en_10',          '英语启蒙',     'mastery', '掌握 10 个英语单词',       'globe',  '{"type":"mastery_count","subject":"english","threshold":10}'::jsonb,              100),
    ('en_50',          '单词小行家',   'mastery', '掌握 50 个英语单词',       'globe',  '{"type":"mastery_count","subject":"english","threshold":50}'::jsonb,              110),
    ('math_10',        '数学小星星',   'mastery', '掌握 10 个数学技能',       'abacus', '{"type":"mastery_count","subject":"math","threshold":10}'::jsonb,                  120),
    ('math_50',        '心算高手',     'mastery', '掌握 50 个数学技能',       'abacus', '{"type":"mastery_count","subject":"math","threshold":50}'::jsonb,                  130),
    ('star_50',        '星星收藏家',   'star',    '累计获得 50 颗星',         'star',   '{"type":"star_total","threshold":50}'::jsonb,                                      140),
    ('star_200',       '星光闪闪',     'star',    '累计获得 200 颗星',        'star',   '{"type":"star_total","threshold":200}'::jsonb,                                     150),
    ('mastery_100',    '百题通',       'mastery', '累计掌握 100 个知识点',    'trophy', '{"type":"mastery_count","threshold":100}'::jsonb,                                  160)
ON CONFLICT (code) DO UPDATE SET
    name        = EXCLUDED.name,
    category    = EXCLUDED.category,
    description = EXCLUDED.description,
    icon        = EXCLUDED.icon,
    rule        = EXCLUDED.rule,
    sort_order  = EXCLUDED.sort_order,
    active      = true;
