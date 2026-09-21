-- M2 内容底座：内容域全量表 + 学习基准线 curriculum_plan。
--
-- 设计要点：
--  1. knowledge_points 是掌握度的唯一挂载点，汉字/单词/故事各有一条；
--  2. 字典类内容（汉字、组词、英语词）自家管线产出，导入即 published；
--     采集类内容（故事）默认 pending，必须家长过一遍审核队列才上线；
--  3. content_hash 保证重跑导入不产生重复行（幂等）。

CREATE TABLE knowledge_points (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_code text        NOT NULL REFERENCES subjects(code) ON DELETE RESTRICT,
    stage_code   text        REFERENCES stages(code) ON DELETE SET NULL,
    kind         text        NOT NULL,
    ref_id       uuid,
    code         text        NOT NULL UNIQUE,
    name         text        NOT NULL,
    difficulty   smallint    NOT NULL DEFAULT 1,
    metadata     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status       text        NOT NULL DEFAULT 'published',
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT kp_kind_valid       CHECK (kind IN ('hanzi', 'word', 'story', 'math_skill', 'sentence')),
    CONSTRAINT kp_difficulty_valid CHECK (difficulty BETWEEN 0 AND 9),
    CONSTRAINT kp_status_valid     CHECK (status IN ('published', 'archived'))
);

COMMENT ON TABLE knowledge_points IS '掌握度挂载点：每个汉字/单词/题型档/故事一条，SM-2 状态机只认这张表';
COMMENT ON COLUMN knowledge_points.code IS '稳定业务码（hanzi:一 / word:cat），重跑导入按同一码去重';
COMMENT ON COLUMN knowledge_points.ref_id IS '指向 hanzi/en_words/stories 等表，跨表故不加外键';

CREATE INDEX IF NOT EXISTS idx_kp_stage ON knowledge_points (subject_code, stage_code, kind);
CREATE INDEX IF NOT EXISTS idx_kp_ref ON knowledge_points (ref_id);

CREATE TABLE hanzi (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    "char"         text        NOT NULL UNIQUE,
    kp_id          uuid        REFERENCES knowledge_points(id) ON DELETE SET NULL,
    pinyin         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    radical        text,
    stroke_count   smallint,
    stroke_paths   jsonb,
    has_anim       boolean     NOT NULL DEFAULT false,
    explanation    text,
    stage_code     text        REFERENCES stages(code) ON DELETE SET NULL,
    order_in_stage int         NOT NULL DEFAULT 0,
    source         text        NOT NULL DEFAULT 'import',
    status         text        NOT NULL DEFAULT 'published',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT hanzi_status_valid CHECK (status IN ('pending', 'published', 'rejected', 'archived'))
);

COMMENT ON TABLE hanzi IS '汉字字典：拼音/部首/笔画/笔顺（hanzi-writer 原始 JSON）/所属阶段';
COMMENT ON COLUMN hanzi.stroke_paths IS 'hanzi-writer 字形数据（strokes + medians），为空表示缺字形，前端退回静态字图';
COMMENT ON COLUMN hanzi.order_in_stage IS '阶段内顺序，决定新学顺序与 curriculum_plan 铺线顺序';

CREATE INDEX IF NOT EXISTS idx_hanzi_stage ON hanzi (stage_code, order_in_stage);
CREATE INDEX IF NOT EXISTS idx_hanzi_status ON hanzi (status);

CREATE TABLE hanzi_words (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hanzi_id   uuid        NOT NULL REFERENCES hanzi(id) ON DELETE CASCADE,
    word       text        NOT NULL,
    pinyin     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    sort_order int         NOT NULL DEFAULT 0,
    stage_ok   boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT hanzi_words_unique UNIQUE (hanzi_id, word)
);

COMMENT ON TABLE hanzi_words IS '控字组词：每字 6 条，只用已学阶段的字；stage_ok 由家长复核后置位';

CREATE INDEX IF NOT EXISTS idx_hanzi_words_hanzi ON hanzi_words (hanzi_id, sort_order);

CREATE TABLE en_words (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kp_id         uuid        REFERENCES knowledge_points(id) ON DELETE SET NULL,
    word          text        NOT NULL UNIQUE,
    topic         text,
    phonetic      text,
    pos           text,
    meaning_zh    text,
    definition_en text,
    example_en    text,
    example_zh    text,
    frq           int         NOT NULL DEFAULT 0,
    level_code    text        REFERENCES stages(code) ON DELETE SET NULL,
    image_url     text,
    audio_url     text,
    source        text        NOT NULL DEFAULT 'ecdict',
    status        text        NOT NULL DEFAULT 'published',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT en_words_status_valid CHECK (status IN ('pending', 'published', 'rejected', 'archived'))
);

COMMENT ON TABLE en_words IS '英语单词：ECDICT 释义 + 自有词频分级 + E1-E10 级别（frq 越小越常用）';
COMMENT ON COLUMN en_words.frq IS 'COCA 词频序号，升序即越常用；缺失记 0';

CREATE INDEX IF NOT EXISTS idx_en_words_level ON en_words (level_code, frq);
CREATE INDEX IF NOT EXISTS idx_en_words_topic ON en_words (topic);

CREATE TABLE en_sentences (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kp_id      uuid        REFERENCES knowledge_points(id) ON DELETE SET NULL,
    text       text        NOT NULL UNIQUE,
    translation text,
    level_code text        REFERENCES stages(code) ON DELETE SET NULL,
    audio_url  text,
    status     text        NOT NULL DEFAULT 'published',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT en_sentences_status_valid CHECK (status IN ('pending', 'published', 'rejected', 'archived'))
);

COMMENT ON TABLE en_sentences IS '英语句型库：E10 的 ~130 句句型，M3 起用于句型填空与跟读';

CREATE TABLE stories (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lang         text        NOT NULL DEFAULT 'zh',
    title        text        NOT NULL,
    summary      text,
    body_md      text        NOT NULL,
    category     text,
    age_group    text,
    level_code   text        REFERENCES stages(code) ON DELETE SET NULL,
    char_count   int         NOT NULL DEFAULT 0,
    word_count   int         NOT NULL DEFAULT 0,
    cover_url    text,
    images       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    new_chars    jsonb       NOT NULL DEFAULT '[]'::jsonb,
    questions    jsonb       NOT NULL DEFAULT '[]'::jsonb,
    discussion   jsonb       NOT NULL DEFAULT '[]'::jsonb,
    unsafe_hits  jsonb       NOT NULL DEFAULT '[]'::jsonb,
    suitable     boolean     NOT NULL DEFAULT true,
    content_hash text        NOT NULL UNIQUE,
    source       text        NOT NULL DEFAULT 'import',
    source_ref   text,
    source_url   text,
    license      text,
    status       text        NOT NULL DEFAULT 'pending',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT stories_lang_valid   CHECK (lang IN ('zh', 'en')),
    CONSTRAINT stories_status_valid CHECK (status IN ('pending', 'published', 'rejected', 'archived'))
);

COMMENT ON TABLE stories IS '故事：中文 17,250（采集）+ 中英双语 1,663；默认 pending，需家长过审核队列';
COMMENT ON COLUMN stories.suitable IS '适宜性筛查结果：命中不宜词库为 false，孩子端默认屏蔽，家长可逐条解除';
COMMENT ON COLUMN stories.content_hash IS '正文 SHA-256，导入幂等键';

CREATE INDEX IF NOT EXISTS idx_stories_browse ON stories (lang, level_code, suitable, status);
CREATE INDEX IF NOT EXISTS idx_stories_status ON stories (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stories_category ON stories (category);

CREATE TABLE story_pairs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         text        NOT NULL UNIQUE,
    zh_story_id  uuid        NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    en_story_id  uuid        NOT NULL UNIQUE REFERENCES stories(id) ON DELETE CASCADE,
    age_group    text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE story_pairs IS '双语配对：中英一一对应，支撑中/英/对照三种阅读模式';

CREATE TABLE videos (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title        text        NOT NULL,
    source_type  text        NOT NULL DEFAULT 'bilibili',
    bvid         text,
    source_url   text        NOT NULL,
    duration_sec int,
    level_code   text        REFERENCES stages(code) ON DELETE SET NULL,
    cover_url    text,
    subtitle_url text,
    added_by     uuid        REFERENCES parents(id) ON DELETE SET NULL,
    status       text        NOT NULL DEFAULT 'pending',
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT videos_source_valid CHECK (source_type IN ('bilibili', 'upload')),
    CONSTRAINT videos_status_valid CHECK (status IN ('pending', 'published', 'rejected', 'archived'))
);

COMMENT ON TABLE videos IS '视频白名单：只存元数据，不下载；孩子端仅家长添加过的可见';

CREATE INDEX IF NOT EXISTS idx_videos_status ON videos (status, created_at DESC);

CREATE TABLE math_templates (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code             text        NOT NULL UNIQUE,
    question_type    text        NOT NULL,
    difficulty_band  text        REFERENCES stages(code) ON DELETE SET NULL,
    generator_config jsonb       NOT NULL DEFAULT '{}'::jsonb,
    display_config   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status           text        NOT NULL DEFAULT 'pending',
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT math_templates_status_valid CHECK (status IN ('pending', 'published', 'rejected', 'archived'))
);

COMMENT ON TABLE math_templates IS '数学题模板：模板 + 参数化生成（seed 可复现），M3 起启用';

CREATE TABLE content_review (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    content_type text        NOT NULL,
    ref_table    text        NOT NULL,
    ref_id       uuid        NOT NULL,
    title        text,
    summary      text,
    source       text,
    source_url   text,
    hits         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    status       text        NOT NULL DEFAULT 'pending',
    reviewer_id  uuid        REFERENCES parents(id) ON DELETE SET NULL,
    reviewed_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT content_review_type_valid   CHECK (content_type IN ('story', 'hanzi_word', 'en_word')),
    CONSTRAINT content_review_status_valid CHECK (status IN ('pending', 'approved', 'rejected')),
    CONSTRAINT content_review_unique UNIQUE (ref_table, ref_id)
);

COMMENT ON TABLE content_review IS '审核队列：采集/导入内容默认进队，家长通过后目标表转 published';

CREATE INDEX IF NOT EXISTS idx_review_queue ON content_review (status, content_type, created_at DESC);

CREATE TABLE curriculum_plan (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    child_id          uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    kp_id             uuid        NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    subject_code      text        NOT NULL REFERENCES subjects(code) ON DELETE CASCADE,
    stage_code        text        REFERENCES stages(code) ON DELETE SET NULL,
    planned_day_index int         NOT NULL,
    planned_date      date        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT curriculum_plan_unique UNIQUE (child_id, kp_id),
    CONSTRAINT curriculum_plan_day_valid CHECK (planned_day_index > 0)
);

COMMENT ON TABLE curriculum_plan IS '标准节奏基准线：语文 6 字/天、英语 5 词/天铺 Day 1..N，只作参照不设闸门';
COMMENT ON COLUMN curriculum_plan.planned_date IS '按标准节奏推算的计划日期，与实际进度相减即偏差天数';

CREATE INDEX IF NOT EXISTS idx_plan_child ON curriculum_plan (child_id, subject_code, planned_day_index);
