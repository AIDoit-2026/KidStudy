-- 学习域查询（sqlc 生成到 internal/dbgen）：掌握度、错题本、会话、答题流水、日汇总、专项。
--
-- 约定与 content.sql 一致：列表查询「过滤器为空即不过滤」；时间一律用参数传入，
-- 不在 SQL 里用 now()，方便测试与「逾期时长」排序复用同一个时间戳。

-- ---------------------------------------------------------------- 掌握度

-- name: GetMastery :one
SELECT id, child_id, kp_id, level, ease, interval_hours, next_review_at, difficulty,
       correct_count, wrong_count, streak, wrong_streak, last_result,
       first_learned_at, mastered_at, attempts, attempts_to_master, repeat_days, last_review_date
FROM mastery_records
WHERE child_id = $1 AND kp_id = $2;

-- name: ListMasteryByKPs :many
SELECT id, child_id, kp_id, level, ease, interval_hours, next_review_at, difficulty,
       correct_count, wrong_count, streak, wrong_streak, last_result,
       first_learned_at, mastered_at, attempts, attempts_to_master, repeat_days, last_review_date
FROM mastery_records
WHERE child_id = $1 AND kp_id = ANY(sqlc.arg(kp_ids)::uuid[]);

-- name: UpsertMastery :one
INSERT INTO mastery_records (child_id, kp_id, level, ease, interval_hours, next_review_at, difficulty,
                             correct_count, wrong_count, streak, wrong_streak, last_result,
                             first_learned_at, mastered_at, attempts, attempts_to_master,
                             repeat_days, last_review_date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
ON CONFLICT (child_id, kp_id) DO UPDATE SET
    level              = EXCLUDED.level,
    ease               = EXCLUDED.ease,
    interval_hours     = EXCLUDED.interval_hours,
    next_review_at     = EXCLUDED.next_review_at,
    difficulty         = EXCLUDED.difficulty,
    correct_count      = EXCLUDED.correct_count,
    wrong_count        = EXCLUDED.wrong_count,
    streak             = EXCLUDED.streak,
    wrong_streak       = EXCLUDED.wrong_streak,
    last_result        = EXCLUDED.last_result,
    first_learned_at   = COALESCE(mastery_records.first_learned_at, EXCLUDED.first_learned_at),
    mastered_at        = COALESCE(mastery_records.mastered_at, EXCLUDED.mastered_at),
    attempts           = EXCLUDED.attempts,
    attempts_to_master = EXCLUDED.attempts_to_master,
    repeat_days        = EXCLUDED.repeat_days,
    last_review_date   = EXCLUDED.last_review_date,
    updated_at         = now()
RETURNING id, child_id, kp_id, level, ease, interval_hours, next_review_at, difficulty,
          correct_count, wrong_count, streak, wrong_streak, last_result,
          first_learned_at, mastered_at, attempts, attempts_to_master, repeat_days, last_review_date;

-- name: DeleteMastery :exec
DELETE FROM mastery_records WHERE child_id = $1 AND kp_id = $2;

-- 到期复习：逾期越久越靠前，同逾期程度下掌握度低的优先（§4.2 步骤 2）
-- name: ListDueReviews :many
SELECT m.id, m.child_id, m.kp_id, m.level, m.ease, m.interval_hours, m.next_review_at, m.difficulty,
       m.correct_count, m.wrong_count, m.streak, m.wrong_streak, m.last_result,
       m.first_learned_at, m.mastered_at, m.attempts, m.attempts_to_master, m.repeat_days, m.last_review_date,
       k.subject_code, k.kind, k.code, k.name
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND m.level > 0
  AND m.next_review_at <= sqlc.arg(now_ts)
  AND (sqlc.arg(subject_code)::text = '' OR k.subject_code = sqlc.arg(subject_code)::text)
ORDER BY (sqlc.arg(now_ts) - m.next_review_at) DESC, m.level ASC, m.kp_id
LIMIT sqlc.arg(lim);

-- name: CountDueReviews :one
SELECT count(*)::bigint
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND m.level > 0
  AND m.next_review_at <= sqlc.arg(now_ts)
  AND (sqlc.arg(subject_code)::text = '' OR k.subject_code = sqlc.arg(subject_code)::text);

-- 新学候选（基准线驱动）：按标准节奏的 Day 序号取还没学过的 kp（§4.2 步骤 4）
-- name: ListNewKPsByPlan :many
SELECT k.id, k.subject_code, k.stage_code, k.kind, k.code, k.name, k.difficulty, k.metadata,
       p.planned_day_index
FROM curriculum_plan p
JOIN knowledge_points k ON k.id = p.kp_id
LEFT JOIN mastery_records m ON m.child_id = p.child_id AND m.kp_id = p.kp_id
WHERE p.child_id = sqlc.arg(child_id)
  AND p.subject_code = sqlc.arg(subject_code)
  AND m.id IS NULL
  AND k.status = 'published'
  AND (sqlc.arg(kind)::text = '' OR k.kind = sqlc.arg(kind)::text)
ORDER BY p.planned_day_index, k.code
LIMIT sqlc.arg(lim);

-- 新学候选（阶段兜底）：孩子没有基准线时按阶段顺序取，保证新孩子也能开局
-- name: ListNewKPsByStage :many
SELECT k.id, k.subject_code, k.stage_code, k.kind, k.code, k.name, k.difficulty, k.metadata,
       0::int AS planned_day_index
FROM knowledge_points k
WHERE k.subject_code = sqlc.arg(subject_code)
  AND k.status = 'published'
  AND (sqlc.arg(stage_code)::text = '' OR k.stage_code = sqlc.arg(stage_code)::text)
  AND (sqlc.arg(kind)::text = '' OR k.kind = sqlc.arg(kind)::text)
  AND NOT EXISTS (SELECT 1 FROM mastery_records m WHERE m.child_id = sqlc.arg(child_id) AND m.kp_id = k.id)
ORDER BY k.stage_code, k.difficulty, k.code
LIMIT sqlc.arg(lim);

-- name: CountMastered :one
SELECT count(*)::bigint
FROM mastery_records
WHERE child_id = $1 AND level >= 3;

-- ---------------------------------------------------------------- 错题本

-- name: ListWrongBook :many
SELECT w.id, w.child_id, w.kp_id, w.added_at, w.cleared_at, w.consecutive_correct, w.wrong_count,
       k.subject_code, k.kind, k.code, k.name
FROM wrong_book_entries w
JOIN knowledge_points k ON k.id = w.kp_id
WHERE w.child_id = sqlc.arg(child_id)
  AND (NOT sqlc.arg(open_only)::boolean OR w.cleared_at IS NULL)
ORDER BY w.cleared_at NULLS FIRST, w.added_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountWrongBook :one
SELECT count(*)::bigint
FROM wrong_book_entries
WHERE child_id = sqlc.arg(child_id)
  AND (NOT sqlc.arg(open_only)::boolean OR cleared_at IS NULL);

-- name: GetWrongEntry :one
SELECT id, child_id, kp_id, added_at, cleared_at, consecutive_correct, wrong_count
FROM wrong_book_entries
WHERE child_id = $1 AND kp_id = $2;

-- name: UpsertWrongEntry :one
INSERT INTO wrong_book_entries (child_id, kp_id, cleared_at, consecutive_correct, wrong_count)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (child_id, kp_id) DO UPDATE SET
    cleared_at          = EXCLUDED.cleared_at,
    consecutive_correct = EXCLUDED.consecutive_correct,
    -- 只在「再次答错」时累加错误次数并重置入本时间；单纯移出错题本不该动这两个字段
    wrong_count         = CASE WHEN EXCLUDED.cleared_at IS NULL
                               THEN wrong_book_entries.wrong_count + 1
                               ELSE wrong_book_entries.wrong_count END,
    added_at            = CASE WHEN EXCLUDED.cleared_at IS NULL THEN now() ELSE wrong_book_entries.added_at END,
    updated_at          = now()
RETURNING id, child_id, kp_id, added_at, cleared_at, consecutive_correct, wrong_count;

-- name: DeleteWrongEntry :exec
DELETE FROM wrong_book_entries WHERE id = $1 AND child_id = $2;

-- ---------------------------------------------------------------- 会话

-- name: InsertSession :one
INSERT INTO learning_sessions (child_id, device_type)
VALUES ($1, $2)
RETURNING id, child_id, device_type, started_at, ended_at, duration_sec, question_count,
          answered_count, correct_count, skipped_count, star_count, completed_by,
          parent_score, parent_note, status;

-- name: GetSession :one
SELECT id, child_id, device_type, started_at, ended_at, duration_sec, question_count,
       answered_count, correct_count, skipped_count, star_count, completed_by,
       parent_score, parent_note, status
FROM learning_sessions
WHERE id = $1 AND child_id = $2;

-- name: FinishSession :one
UPDATE learning_sessions SET
    ended_at       = sqlc.arg(now_ts),
    duration_sec   = sqlc.arg(duration_sec),
    question_count = sqlc.arg(question_count),
    answered_count = sqlc.arg(answered_count),
    correct_count  = sqlc.arg(correct_count),
    skipped_count  = sqlc.arg(skipped_count),
    star_count     = sqlc.arg(star_count),
    completed_by   = sqlc.arg(completed_by),
    status         = 'finished',
    updated_at     = now()
WHERE id = sqlc.arg(id) AND child_id = sqlc.arg(child_id) AND status = 'active'
RETURNING id, child_id, device_type, started_at, ended_at, duration_sec, question_count,
          answered_count, correct_count, skipped_count, star_count, completed_by,
          parent_score, parent_note, status;

-- 家长确认/补录：主观项评分写这里，客观计数一个都不动（§4.11 家长评分不污染客观正确率）
-- name: ConfirmSession :one
UPDATE learning_sessions SET
    parent_score = sqlc.arg(parent_score),
    parent_note  = sqlc.arg(parent_note),
    completed_by = COALESCE(sqlc.arg(completed_by), learning_sessions.completed_by),
    status       = CASE WHEN status = 'active' THEN 'finished' ELSE status END,
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND child_id = sqlc.arg(child_id)
RETURNING id, child_id, device_type, started_at, ended_at, duration_sec, question_count,
          answered_count, correct_count, skipped_count, star_count, completed_by,
          parent_score, parent_note, status;

-- 今日已用秒数：未结束的会话按「到现在」计，保证额度校验不会因忘记 finish 而失效
-- name: TodayUsedSeconds :one
SELECT COALESCE(sum(CASE
        WHEN ended_at IS NULL THEN EXTRACT(EPOCH FROM (sqlc.arg(now_ts) - started_at))
        ELSE duration_sec
    END), 0)::bigint
FROM learning_sessions
WHERE child_id = sqlc.arg(child_id)
  AND status <> 'expired'
  AND started_at >= sqlc.arg(day_start)
  AND started_at < sqlc.arg(day_start) + interval '1 day';

-- name: InsertSessionItem :one
INSERT INTO session_items (session_id, seq, kp_id, subject_code, stage_code,
                           question_type, difficulty, question_snapshot, answer_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, session_id, seq, kp_id, subject_code, stage_code, question_type, difficulty,
          question_snapshot, answer_key, state, is_correct, used_hint, elapsed_ms, answered_at;

-- name: ListSessionItems :many
SELECT id, session_id, seq, kp_id, subject_code, stage_code, question_type, difficulty,
       question_snapshot, answer_key, state, is_correct, used_hint, elapsed_ms, answered_at
FROM session_items
WHERE session_id = $1
ORDER BY seq;

-- name: GetSessionItem :one
SELECT id, session_id, seq, kp_id, subject_code, stage_code, question_type, difficulty,
       question_snapshot, answer_key, state, is_correct, used_hint, elapsed_ms, answered_at
FROM session_items
WHERE id = $1 AND session_id = $2;

-- 只认 pending → 重复提交同一题不会二次计分
-- name: UpdateItemAnswer :exec
UPDATE session_items SET
    state       = sqlc.arg(state),
    is_correct  = sqlc.arg(is_correct),
    used_hint   = sqlc.arg(used_hint),
    elapsed_ms  = sqlc.arg(elapsed_ms),
    answered_at = sqlc.arg(now_ts)
WHERE id = sqlc.arg(id) AND session_id = sqlc.arg(session_id) AND state = 'pending';

-- name: SessionCounts :one
SELECT count(*)::bigint                                          AS total,
       count(*) FILTER (WHERE state = 'answered')::bigint        AS answered,
       count(*) FILTER (WHERE state = 'answered' AND is_correct)::bigint AS correct,
       count(*) FILTER (WHERE state = 'skipped')::bigint         AS skipped,
       count(*) FILTER (WHERE used_hint)::bigint                 AS used_hint
FROM session_items
WHERE session_id = $1;

-- ---------------------------------------------------------------- 答题流水

-- name: InsertAnswerLog :exec
INSERT INTO answer_logs (child_id, session_id, item_id, kp_id, subject_code, question_type,
                         is_correct, used_hint, elapsed_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- 难度自适应用：取最近 n 次作答的正确与否与用时（§4.2「连 3 次正确率<60% 降档」）
-- name: RecentAnswers :many
SELECT is_correct, elapsed_ms
FROM answer_logs
WHERE child_id = $1 AND kp_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- ---------------------------------------------------------------- 日汇总

-- 增量累加，会话结束时调用一次（量小，不进 worker）
-- name: UpsertDailyStat :one
INSERT INTO daily_stats (child_id, stat_date, subject_code, duration_sec, question_count,
                         correct_count, new_mastered, star_count, actual_new, repeat_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (child_id, stat_date, subject_code) DO UPDATE SET
    duration_sec   = daily_stats.duration_sec + EXCLUDED.duration_sec,
    question_count = daily_stats.question_count + EXCLUDED.question_count,
    correct_count  = daily_stats.correct_count + EXCLUDED.correct_count,
    new_mastered   = daily_stats.new_mastered + EXCLUDED.new_mastered,
    star_count     = daily_stats.star_count + EXCLUDED.star_count,
    actual_new     = daily_stats.actual_new + EXCLUDED.actual_new,
    repeat_count   = daily_stats.repeat_count + EXCLUDED.repeat_count,
    updated_at     = now()
RETURNING id, child_id, stat_date, subject_code, duration_sec, question_count, correct_count,
          new_mastered, star_count, planned_new, actual_new, repeat_count;

-- 家长控制：额度与开关（§4.2 步骤 1）。查不到就用默认值，不报错。
-- name: GetParentSettings :one
SELECT parent_id, daily_limit_min, session_limit_min, rest_interval_min, subject_switches,
       require_parent_confirm, pace_mode
FROM parent_settings
WHERE parent_id = $1;

-- 本次会话里「今天第一次学到」的知识点数，用于 daily_stats.actual_new
-- name: CountNewLearnedToday :one
SELECT count(*)::bigint
FROM mastery_records
WHERE child_id = sqlc.arg(child_id)
  AND kp_id = ANY(sqlc.arg(kp_ids)::uuid[])
  AND first_learned_at >= sqlc.arg(day_start)
  AND first_learned_at < sqlc.arg(day_start) + interval '1 day';

-- ---------------------------------------------------------------- 专项指派

-- name: ListPendingAssignments :many
SELECT a.id, a.child_id, a.kp_id, a.reason,
       k.subject_code, k.kind, k.code, k.name
FROM practice_assignments a
JOIN knowledge_points k ON k.id = a.kp_id
WHERE a.child_id = sqlc.arg(child_id) AND a.status = 'pending'
ORDER BY a.created_at
LIMIT sqlc.arg(lim);

-- name: UpsertAssignment :one
INSERT INTO practice_assignments (child_id, kp_id, parent_id, reason)
VALUES ($1, $2, $3, $4)
ON CONFLICT (child_id, kp_id) DO UPDATE SET
    reason   = EXCLUDED.reason,
    status   = 'pending',
    used_at  = NULL,
    parent_id = EXCLUDED.parent_id
RETURNING id, child_id, kp_id, parent_id, reason, status, created_at;

-- name: MarkAssignmentDone :exec
UPDATE practice_assignments SET status = 'done', used_at = now()
WHERE child_id = $1 AND kp_id = ANY(sqlc.arg(kp_ids)::uuid[]);

-- name: DeleteAssignment :exec
DELETE FROM practice_assignments WHERE id = $1 AND child_id = $2;

-- ---------------------------------------------------------------- 组卷素材

-- name: ListKPsByIDs :many
SELECT id, subject_code, stage_code, kind, code, name, difficulty, metadata
FROM knowledge_points
WHERE id = ANY(sqlc.arg(kp_ids)::uuid[]);

-- name: LoadHanziByKPs :many
SELECT k.id AS kp_id, h.id AS hanzi_id, h."char", h.pinyin, h.radical, h.stroke_count,
       h.stroke_paths, h.has_anim, h.explanation, h.stage_code, h.order_in_stage
FROM knowledge_points k
JOIN hanzi h ON h.id = k.ref_id
WHERE k.id = ANY(sqlc.arg(kp_ids)::uuid[]) AND k.kind = 'hanzi';

-- name: LoadEnWordsByKPs :many
SELECT k.id AS kp_id, w.id AS word_id, w.word, w.topic, w.phonetic, w.pos, w.meaning_zh,
       w.definition_en, w.example_en, w.example_zh, w.frq, w.level_code, w.image_url
FROM knowledge_points k
JOIN en_words w ON w.id = k.ref_id
WHERE k.id = ANY(sqlc.arg(kp_ids)::uuid[]) AND k.kind = 'word';

-- name: LoadMathTemplatesByKPs :many
SELECT k.id AS kp_id, t.code AS template_code, t.difficulty_band, t.generator_config, t.display_config
FROM knowledge_points k
JOIN math_templates t ON t.id = k.ref_id
WHERE k.id = ANY(sqlc.arg(kp_ids)::uuid[]) AND k.kind = 'math_skill';

-- name: LoadStoriesByKPs :many
SELECT k.id AS kp_id, s.id AS story_id, s.title, s.summary, s.lang, s.level_code
FROM knowledge_points k
JOIN stories s ON s.id = k.ref_id
WHERE k.id = ANY(sqlc.arg(kp_ids)::uuid[]) AND k.kind = 'story';

-- name: ListMathTemplates :many
SELECT code, question_type, difficulty_band, generator_config, display_config
FROM math_templates
WHERE status = 'published'
ORDER BY code;

-- name: GetMathTemplateByCode :one
SELECT code, question_type, difficulty_band, generator_config, display_config
FROM math_templates
WHERE code = sqlc.arg(code) AND status = 'published';
