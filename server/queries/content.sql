-- 内容模块的读查询（sqlc 生成到 internal/feature/content/dbgen）。
--
-- 约定：
--  1. 列表查询一律「过滤器为空字符串即不过滤」，避免为每种组合写一条 SQL；
--  2. pinyin / stroke_paths / images 等 jsonb 列在 repository 层再解析成领域结构；
--  3. 批量导入走 internal/feature/content/bulk.go 的多行 upsert（sqlc 不做批量写）。

-- name: ListStages :many
SELECT code, subject_code, name, sort_order, target_count
FROM stages
WHERE (sqlc.arg(subject_code)::text = '' OR subject_code = sqlc.arg(subject_code)::text)
ORDER BY subject_code, sort_order;

-- name: UpsertStage :exec
INSERT INTO stages (code, subject_code, name, sort_order, target_count)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (code) DO UPDATE SET
    subject_code = EXCLUDED.subject_code,
    name         = EXCLUDED.name,
    sort_order   = EXCLUDED.sort_order,
    target_count = EXCLUDED.target_count;

-- name: ListHanzi :many
SELECT id, "char", pinyin, radical, stroke_count, has_anim, explanation,
       stage_code, order_in_stage, status
FROM hanzi
WHERE (sqlc.arg(stage_code)::text = '' OR stage_code = sqlc.arg(stage_code)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY stage_code, order_in_stage, "char"
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountHanzi :one
SELECT count(*)::bigint
FROM hanzi
WHERE (sqlc.arg(stage_code)::text = '' OR stage_code = sqlc.arg(stage_code)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

-- name: GetHanziByID :one
SELECT id, "char", kp_id, pinyin, radical, stroke_count, stroke_paths, has_anim,
       explanation, stage_code, order_in_stage, source, status, created_at, updated_at
FROM hanzi
WHERE id = $1;

-- name: GetHanziByChar :one
SELECT id, "char", kp_id, pinyin, radical, stroke_count, stroke_paths, has_anim,
       explanation, stage_code, order_in_stage, source, status, created_at, updated_at
FROM hanzi
WHERE "char" = $1;

-- name: ListHanziWords :many
SELECT id, hanzi_id, word, pinyin, sort_order, stage_ok
FROM hanzi_words
WHERE hanzi_id = ANY(sqlc.arg(hanzi_ids)::uuid[])
ORDER BY hanzi_id, sort_order;

-- name: ListEnWords :many
SELECT id, word, topic, phonetic, pos, meaning_zh, definition_en, example_en, example_zh,
       frq, level_code, image_url, audio_url, status
FROM en_words
WHERE (sqlc.arg(level_code)::text = '' OR level_code = sqlc.arg(level_code)::text)
  AND (sqlc.arg(topic)::text = '' OR topic = sqlc.arg(topic)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY frq, word
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountEnWords :one
SELECT count(*)::bigint
FROM en_words
WHERE (sqlc.arg(level_code)::text = '' OR level_code = sqlc.arg(level_code)::text)
  AND (sqlc.arg(topic)::text = '' OR topic = sqlc.arg(topic)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

-- name: ListStories :many
SELECT id, lang, title, summary, category, age_group, level_code, char_count, word_count,
       cover_url, suitable, status, source, source_url, created_at
FROM stories
WHERE (sqlc.arg(lang)::text = '' OR lang = sqlc.arg(lang)::text)
  AND (sqlc.arg(level_code)::text = '' OR level_code = sqlc.arg(level_code)::text)
  AND (sqlc.arg(category)::text = '' OR category = sqlc.arg(category)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (NOT sqlc.arg(suitable_only)::boolean OR suitable)
ORDER BY level_code, title
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountStories :one
SELECT count(*)::bigint
FROM stories
WHERE (sqlc.arg(lang)::text = '' OR lang = sqlc.arg(lang)::text)
  AND (sqlc.arg(level_code)::text = '' OR level_code = sqlc.arg(level_code)::text)
  AND (sqlc.arg(category)::text = '' OR category = sqlc.arg(category)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (NOT sqlc.arg(suitable_only)::boolean OR suitable);

-- name: GetStoryByID :one
SELECT id, lang, title, summary, body_md, category, age_group, level_code, char_count, word_count,
       cover_url, images, new_chars, questions, discussion, unsafe_hits, suitable,
       source, source_ref, source_url, license, status, created_at, updated_at
FROM stories
WHERE id = $1;

-- name: GetStoryPair :one
SELECT p.id, p.slug, p.age_group,
       p.zh_story_id, zh.title AS zh_title, zh.level_code AS zh_level_code, zh.body_md AS zh_body_md,
       p.en_story_id, en.title AS en_title, en.level_code AS en_level_code, en.body_md AS en_body_md
FROM story_pairs p
JOIN stories zh ON zh.id = p.zh_story_id
JOIN stories en ON en.id = p.en_story_id
WHERE p.zh_story_id = $1 OR p.en_story_id = $1;

-- name: ListReviewQueue :many
SELECT r.id, r.content_type, r.ref_table, r.ref_id, r.title, r.summary, r.source, r.source_url,
       r.hits, r.status, r.reviewed_at, r.created_at,
       s.level_code, s.char_count, s.suitable
FROM content_review r
LEFT JOIN stories s ON s.id = r.ref_id
WHERE (sqlc.arg(status)::text = '' OR r.status = sqlc.arg(status)::text)
  AND (sqlc.arg(content_type)::text = '' OR r.content_type = sqlc.arg(content_type)::text)
ORDER BY r.created_at DESC, r.id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountReviewQueue :one
SELECT count(*)::bigint
FROM content_review r
WHERE (sqlc.arg(status)::text = '' OR r.status = sqlc.arg(status)::text)
  AND (sqlc.arg(content_type)::text = '' OR r.content_type = sqlc.arg(content_type)::text);

-- name: GetReviewByID :one
SELECT id, content_type, ref_table, ref_id, title, summary, source, source_url, hits,
       status, reviewer_id, reviewed_at, created_at
FROM content_review
WHERE id = $1;

-- name: DecideReview :one
UPDATE content_review
SET status = $3, reviewer_id = $4, reviewed_at = now()
WHERE id = $1 AND status = $2
RETURNING id, content_type, ref_table, ref_id, status;

-- name: PublishStory :execrows
UPDATE stories SET status = 'published', updated_at = now()
WHERE id = $1 AND status = 'pending';

-- name: RejectStory :execrows
UPDATE stories SET status = 'rejected', updated_at = now()
WHERE id = $1 AND status = 'pending';

-- name: ListChildrenForPlan :many
SELECT id, stage_code, created_at
FROM children
ORDER BY created_at;

-- name: ListPlannedKPs :many
SELECT kp.id, kp.subject_code, kp.stage_code, kp.kind, s.sort_order AS stage_order,
       COALESCE(h.order_in_stage, 0) AS order_in_stage
FROM knowledge_points kp
JOIN stages s ON s.code = kp.stage_code
LEFT JOIN hanzi h ON h.kp_id = kp.id
WHERE kp.subject_code = $1
  AND s.sort_order >= COALESCE((SELECT st.sort_order FROM stages st WHERE st.code = $2), 0)
ORDER BY s.sort_order, COALESCE(h.order_in_stage, 0), kp.code;

-- name: CountPlanForChild :one
SELECT count(*)::bigint FROM curriculum_plan WHERE child_id = $1;

-- name: InsertPlanRow :exec
INSERT INTO curriculum_plan (child_id, kp_id, subject_code, stage_code, planned_day_index, planned_date)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (child_id, kp_id) DO NOTHING;
