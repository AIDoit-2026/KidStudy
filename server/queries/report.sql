-- 报表 / 成就 / 日汇总的查询。
--
-- 约定：
--  1. 「学习日」按 Asia/Shanghai 切分（M3 的 daily_stats.stat_date 就是用本机 +08 时区的
--     startOfDay 算的，两边一致才不会出现同一晚跨天的行被算到两天）。
--  2. 报表模块会直接 JOIN knowledge_points 取学科/阶段 —— 读报表天然需要多表聚合，
--     走 content.Service 反而要拆成 N 次查询并自带 N+1，这里按「读模型」处理。
--  3. 不排名、不做负向判定：SQL 只出事实，措辞在 service 层统一。

-- ---------------------------------------------------------------- 总览

-- name: ReportMasteryTotals :one
SELECT
    count(*)::bigint                                          AS mastered_total,
    count(*) FILTER (WHERE k.subject_code = 'chinese')::bigint AS mastered_chinese,
    count(*) FILTER (WHERE k.subject_code = 'math')::bigint    AS mastered_math,
    count(*) FILTER (WHERE k.subject_code = 'english')::bigint AS mastered_english,
    COALESCE(sum(m.attempts_to_master), 0)::bigint             AS attempts_to_master_sum
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id) AND m.level >= 3;

-- name: ReportTotals :one
SELECT
    COALESCE(sum(duration_sec), 0)::bigint   AS duration_sec,
    COALESCE(sum(question_count), 0)::bigint AS question_count,
    COALESCE(sum(correct_count), 0)::bigint  AS correct_count,
    COALESCE(sum(star_count), 0)::bigint     AS star_count,
    count(*) FILTER (WHERE question_count > 0)::bigint AS active_days
FROM daily_stats
WHERE child_id = sqlc.arg(child_id) AND subject_code = '';

-- name: ReportSessionTotals :one
SELECT
    count(*)::bigint                                          AS session_count,
    count(*) FILTER (WHERE status = 'finished')::bigint        AS finished_count,
    COALESCE(avg(correct_count::numeric / NULLIF(answered_count, 0)), 0)::float8 AS avg_accuracy
FROM learning_sessions
WHERE child_id = sqlc.arg(child_id);

-- 待复习 / 积压（复习队列长度，用于「设为复习日」建议与总览）
-- name: ReportDueCount :one
SELECT count(*)::bigint
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND m.level > 0
  AND m.next_review_at <= sqlc.arg(now_ts)
  AND (sqlc.arg(subject_code)::text = '' OR k.subject_code = sqlc.arg(subject_code)::text);

-- 分学科进度：已掌握 / 计划总量 / 待复习
-- name: ReportSubjectProgress :many
SELECT
    s.code AS subject_code,
    s.name AS subject_name,
    COALESCE(mp.mastered, 0)::bigint AS mastered,
    COALESCE(pl.total, 0)::bigint    AS planned_total,
    COALESCE(dq.due, 0)::bigint      AS due
FROM subjects s
LEFT JOIN (
    SELECT k.subject_code, count(*) AS mastered
    FROM mastery_records m JOIN knowledge_points k ON k.id = m.kp_id
    WHERE m.child_id = sqlc.arg(child_id) AND m.level >= 3
    GROUP BY k.subject_code
) mp ON mp.subject_code = s.code
LEFT JOIN (
    SELECT subject_code, count(*) AS total FROM curriculum_plan
    WHERE child_id = sqlc.arg(child_id) GROUP BY subject_code
) pl ON pl.subject_code = s.code
LEFT JOIN (
    SELECT k.subject_code, count(*) AS due
    FROM mastery_records m JOIN knowledge_points k ON k.id = m.kp_id
    WHERE m.child_id = sqlc.arg(child_id) AND m.level > 0 AND m.next_review_at <= sqlc.arg(now_ts)
    GROUP BY k.subject_code
) dq ON dq.subject_code = s.code
ORDER BY s.sort_order;

-- 分阶段进度（成长树用）：该阶段的计划量 / 已掌握量
-- name: ReportStageProgress :many
SELECT
    k.stage_code,
    COALESCE(count(DISTINCT k.id), 0)::bigint                              AS planned_total,
    COALESCE(count(DISTINCT k.id) FILTER (WHERE m.level >= 3), 0)::bigint  AS mastered
FROM knowledge_points k
LEFT JOIN mastery_records m ON m.kp_id = k.id AND m.child_id = sqlc.arg(child_id)
WHERE k.subject_code = sqlc.arg(subject_code)
  AND k.status = 'published'
  AND k.stage_code IS NOT NULL
GROUP BY k.stage_code
ORDER BY k.stage_code;

-- ---------------------------------------------------------------- 趋势

-- 逐日趋势：返回 '' 合计行与分学科行，service 按需归拢。
-- name: ReportDailySeries :many
SELECT stat_date, subject_code, duration_sec, question_count, correct_count,
       new_mastered, star_count, planned_new, actual_new, repeat_count,
       cum_planned, cum_actual, deviation_days, passed, parent_confirmed
FROM daily_stats
WHERE child_id = sqlc.arg(child_id)
  AND stat_date >= sqlc.arg(from_date)
  AND stat_date <= sqlc.arg(to_date)
  AND (sqlc.arg(subject_code)::text = '' OR subject_code = sqlc.arg(subject_code)::text)
ORDER BY stat_date, subject_code;

-- ---------------------------------------------------------------- 薄弱项（建议规则 1）

-- 某学科最近 N 天里「掌握度还不到 3 且答错过」的知识点，按错误率排序（TopN）
-- name: ReportWeakKPs :many
SELECT
    k.id AS kp_id, k.code, k.name, k.stage_code, k.subject_code,
    count(*)::bigint                            AS attempts,
    count(*) FILTER (WHERE a.is_correct)::bigint AS correct_count,
    COALESCE(max(a.created_at), to_timestamp(0))::timestamptz AS last_answered_at
FROM answer_logs a
JOIN knowledge_points k ON k.id = a.kp_id
LEFT JOIN mastery_records m ON m.child_id = a.child_id AND m.kp_id = a.kp_id
WHERE a.child_id = sqlc.arg(child_id)
  AND a.created_at >= sqlc.arg(since_ts)
  AND (sqlc.arg(subject_code)::text = '' OR a.subject_code = sqlc.arg(subject_code)::text)
  AND COALESCE(m.level, 0) < 3
GROUP BY k.id, k.code, k.name, k.stage_code, k.subject_code
HAVING count(*) FILTER (WHERE NOT a.is_correct) > 0
ORDER BY (count(*) FILTER (WHERE NOT a.is_correct))::bigint DESC, count(*) DESC
LIMIT sqlc.arg(lim);

-- 逐日逐知识点的正确率（最近 N 天），service 用它判「连续 3 天低正确率」
-- name: ReportKPDailyAccuracy :many
SELECT
    a.kp_id,
    (a.created_at AT TIME ZONE 'Asia/Shanghai')::date AS stat_date,
    count(*)::bigint                                    AS attempts,
    count(*) FILTER (WHERE a.is_correct)::bigint        AS correct_count
FROM answer_logs a
WHERE a.child_id = sqlc.arg(child_id)
  AND a.created_at >= sqlc.arg(since_ts)
  AND (sqlc.arg(subject_code)::text = '' OR a.subject_code = sqlc.arg(subject_code)::text)
GROUP BY a.kp_id, 2
ORDER BY a.kp_id, 2;

-- 薄弱题型 TopN（§4.7 数据源）
-- name: ReportWeakQuestionTypes :many
SELECT
    a.question_type,
    count(*)::bigint                             AS attempts,
    count(*) FILTER (WHERE NOT a.is_correct)::bigint AS wrong_count,
    (count(*) FILTER (WHERE NOT a.is_correct))::float8 / count(*)::float8 AS wrong_rate
FROM answer_logs a
WHERE a.child_id = sqlc.arg(child_id)
  AND a.created_at >= sqlc.arg(since_ts)
  AND a.question_type <> ''
GROUP BY a.question_type
HAVING count(*) FILTER (WHERE NOT a.is_correct) > 0
ORDER BY (count(*) FILTER (WHERE NOT a.is_correct))::bigint DESC, count(*) DESC
LIMIT sqlc.arg(lim);

-- 某学科最近一次学习日期（建议规则 2：连续 5 天未学）
-- name: ReportLastStudyDate :one
SELECT COALESCE(max(a.created_at), to_timestamp(0))::timestamptz AS last_at
FROM answer_logs a
WHERE a.child_id = sqlc.arg(child_id)
  AND a.subject_code = sqlc.arg(subject_code);

-- ---------------------------------------------------------------- 节奏与效率（§4.9）

-- 逐日计划 vs 实际（分学科），pace 报表的双曲线数据源
-- name: ReportPaceSeries :many
SELECT stat_date, subject_code, planned_new, actual_new, repeat_count,
       cum_planned, cum_actual, deviation_days
FROM daily_stats
WHERE child_id = sqlc.arg(child_id)
  AND stat_date >= sqlc.arg(from_date)
  AND stat_date <= sqlc.arg(to_date)
  AND subject_code <> ''
  AND (sqlc.arg(subject_code)::text = '' OR subject_code = sqlc.arg(subject_code)::text)
ORDER BY stat_date, subject_code;

-- 效率趋势：按自然周统计「掌握数 / 总作答数 / 正确率」（近 N 周滚动）
-- name: ReportWeeklyEfficiency :many
SELECT
    date_trunc('week', m.mastered_at AT TIME ZONE 'Asia/Shanghai')::date AS week_start,
    count(*)::bigint                                 AS mastered_count,
    COALESCE(sum(m.attempts_to_master), 0)::bigint   AS attempts_sum,
    COALESCE(avg(m.attempts_to_master), 0)::float8   AS attempts_per_mastery
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND m.mastered_at IS NOT NULL
  AND m.mastered_at >= sqlc.arg(since_ts)
  AND (sqlc.arg(subject_code)::text = '' OR k.subject_code = sqlc.arg(subject_code)::text)
GROUP BY 1
ORDER BY 1;

-- 掌握一个知识点平均要作答几次（§4.9 学习效率口径）
-- name: ReportEfficiencyTotal :one
SELECT
    count(*)::bigint                               AS mastered_count,
    COALESCE(sum(m.attempts_to_master), 0)::bigint AS attempts_sum,
    COALESCE(avg(m.attempts_to_master), 0)::float8 AS attempts_per_mastery
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND m.level >= 3
  AND (sqlc.arg(subject_code)::text = '' OR k.subject_code = sqlc.arg(subject_code)::text);

-- 当前偏差：当日掌握的进度所对应的计划日期（工作日）。
-- offset = 已掌握数 - 1，落在计划顺序上取那一天；service 再算 今天 - planned_date。
-- name: ReportPlannedDateAtProgress :one
SELECT planned_date
FROM curriculum_plan
WHERE child_id = sqlc.arg(child_id)
  AND subject_code = sqlc.arg(subject_code)
ORDER BY planned_day_index
OFFSET GREATEST(sqlc.arg(progress)::int - 1, 0)
LIMIT 1;

-- ---------------------------------------------------------------- 多孩对比（§4.10）

-- 逐日新掌握数（用于按「学习日序号」或自然日累加）
-- name: ReportDailyMastered :many
SELECT
    (m.mastered_at AT TIME ZONE 'Asia/Shanghai')::date AS stat_date,
    count(*)::bigint                                    AS mastered_count
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND m.mastered_at IS NOT NULL
  AND (sqlc.arg(subject_code)::text = '' OR k.subject_code = sqlc.arg(subject_code)::text)
GROUP BY 1
ORDER BY 1;

-- 逐日作答数（对比里的正确率/重复次数按天聚合）
-- name: ReportDailyAnswers :many
SELECT
    (a.created_at AT TIME ZONE 'Asia/Shanghai')::date AS stat_date,
    count(*)::bigint                                   AS attempts,
    count(*) FILTER (WHERE a.is_correct)::bigint        AS correct_count
FROM answer_logs a
WHERE a.child_id = sqlc.arg(child_id)
  AND (sqlc.arg(subject_code)::text = '' OR a.subject_code = sqlc.arg(subject_code)::text)
GROUP BY 1
ORDER BY 1;

-- 第一个学习日（对齐用的起点）
-- name: ReportFirstStudyDate :one
SELECT COALESCE(min((m.first_learned_at AT TIME ZONE 'Asia/Shanghai')::date), '1970-01-01'::date)::date AS first_date
FROM mastery_records m
WHERE m.child_id = sqlc.arg(child_id) AND m.first_learned_at IS NOT NULL;

-- ---------------------------------------------------------------- 成就

-- name: ListBadges :many
SELECT id, code, name, category, description, icon, rule, sort_order
FROM badges
WHERE active
ORDER BY sort_order, code;

-- name: ListChildBadges :many
SELECT cb.id, cb.badge_id, cb.earned_at, cb.progress,
       b.code, b.name, b.category, b.description, b.icon, b.sort_order
FROM child_badges cb
JOIN badges b ON b.id = cb.badge_id
WHERE cb.child_id = sqlc.arg(child_id)
ORDER BY cb.earned_at DESC;

-- 授予（幂等）：已授予过就不动，返回是否本次新发
-- name: InsertChildBadge :one
INSERT INTO child_badges (child_id, badge_id, progress)
VALUES (sqlc.arg(child_id), sqlc.arg(badge_id), sqlc.arg(progress))
ON CONFLICT (child_id, badge_id) DO NOTHING
RETURNING id;

-- 成就评测素材（一次取全，评测器在 Go 侧判定）
-- name: BadgeSessionStats :one
SELECT
    count(*)::bigint                                            AS session_count,
    count(*) FILTER (WHERE answered_count > 0 AND correct_count = answered_count)::bigint AS perfect_sessions,
    COALESCE(sum(star_count), 0)::bigint                        AS star_total
FROM learning_sessions
WHERE child_id = sqlc.arg(child_id) AND status = 'finished';

-- name: BadgeMasteryStats :one
SELECT
    count(*)::bigint                                            AS mastered_total,
    count(*) FILTER (WHERE k.subject_code = 'chinese')::bigint  AS mastered_chinese,
    count(*) FILTER (WHERE k.subject_code = 'math')::bigint     AS mastered_math,
    count(*) FILTER (WHERE k.subject_code = 'english')::bigint  AS mastered_english
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id) AND m.level >= 3;

-- 连续达标天数（从今天往回数，遇到未达标即停）
-- name: ListPassedDaysDesc :many
SELECT stat_date
FROM daily_stats
WHERE child_id = sqlc.arg(child_id)
  AND subject_code = ''
  AND passed
ORDER BY stat_date DESC
LIMIT sqlc.arg(lim);

-- ---------------------------------------------------------------- 日汇总 worker

-- 全量孩子（含活跃与已归档，归档孩子不铺新线但仍可重算历史）
-- name: ListChildIDs :many
SELECT id FROM children ORDER BY created_at;

-- name: RollupPlannedForDate :one
SELECT count(*)::bigint
FROM curriculum_plan
WHERE child_id = sqlc.arg(child_id)
  AND subject_code = sqlc.arg(subject_code)
  AND planned_date = sqlc.arg(stat_date);

-- name: RollupCumPlanned :one
SELECT count(*)::bigint
FROM curriculum_plan
WHERE child_id = sqlc.arg(child_id)
  AND subject_code = sqlc.arg(subject_code)
  AND planned_date <= sqlc.arg(stat_date);

-- name: RollupDailyMastered :one
SELECT count(*)::bigint
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND k.subject_code = sqlc.arg(subject_code)
  AND m.mastered_at IS NOT NULL
  AND (m.mastered_at AT TIME ZONE 'Asia/Shanghai')::date = sqlc.arg(stat_date);

-- name: RollupCumMastered :one
SELECT count(*)::bigint
FROM mastery_records m
JOIN knowledge_points k ON k.id = m.kp_id
WHERE m.child_id = sqlc.arg(child_id)
  AND k.subject_code = sqlc.arg(subject_code)
  AND m.mastered_at IS NOT NULL
  AND (m.mastered_at AT TIME ZONE 'Asia/Shanghai')::date <= sqlc.arg(stat_date);

-- 当日「重复练习」量：当天作答的题里，知识点在当天之前就已经学过（首学日 < 当天）
-- name: RollupRepeatCount :one
SELECT count(*)::bigint
FROM answer_logs a
JOIN mastery_records m ON m.child_id = a.child_id AND m.kp_id = a.kp_id
WHERE a.child_id = sqlc.arg(child_id)
  AND a.subject_code = sqlc.arg(subject_code)
  AND (a.created_at AT TIME ZONE 'Asia/Shanghai')::date = sqlc.arg(stat_date)
  AND m.first_learned_at IS NOT NULL
  AND (m.first_learned_at AT TIME ZONE 'Asia/Shanghai')::date < sqlc.arg(stat_date);

-- 当日该学科的作答与正确数（判达标用，避免与 M3 的增量口径互相打架）
-- name: RollupDailyAnswers :one
SELECT
    count(*)::bigint                            AS attempts,
    count(*) FILTER (WHERE is_correct)::bigint   AS correct_count
FROM answer_logs
WHERE child_id = sqlc.arg(child_id)
  AND subject_code = sqlc.arg(subject_code)
  AND (created_at AT TIME ZONE 'Asia/Shanghai')::date = sqlc.arg(stat_date);

-- 当日是否有家长确认（completed_by 为 parent / mixed 即视为家长参与过确认）
-- name: RollupParentConfirmed :one
SELECT EXISTS (
    SELECT 1 FROM learning_sessions
    WHERE child_id = sqlc.arg(child_id)
      AND completed_by IN ('parent', 'mixed')
      AND (COALESCE(ended_at, started_at) AT TIME ZONE 'Asia/Shanghai')::date = sqlc.arg(stat_date)
)::boolean AS confirmed;

-- 写回汇总行：只覆盖「计划/节奏/达标」这几列，时长与题量仍以会话增量为准。
-- name: UpsertDailyRollup :exec
INSERT INTO daily_stats (
    child_id, stat_date, subject_code, new_mastered, planned_new, actual_new, repeat_count,
    cum_planned, cum_actual, deviation_days, passed, parent_confirmed, updated_at)
VALUES (
    sqlc.arg(child_id), sqlc.arg(stat_date), sqlc.arg(subject_code),
    sqlc.arg(new_mastered), sqlc.arg(planned_new), sqlc.arg(actual_new), sqlc.arg(repeat_count),
    sqlc.arg(cum_planned), sqlc.arg(cum_actual), sqlc.arg(deviation_days),
    sqlc.arg(passed), sqlc.arg(parent_confirmed), now())
ON CONFLICT (child_id, stat_date, subject_code) DO UPDATE SET
    new_mastered     = EXCLUDED.new_mastered,
    planned_new      = EXCLUDED.planned_new,
    actual_new       = EXCLUDED.actual_new,
    repeat_count     = EXCLUDED.repeat_count,
    cum_planned      = EXCLUDED.cum_planned,
    cum_actual       = EXCLUDED.cum_actual,
    deviation_days   = EXCLUDED.deviation_days,
    passed           = EXCLUDED.passed,
    parent_confirmed = EXCLUDED.parent_confirmed,
    updated_at       = now();

-- ---------------------------------------------------------------- 家长设置

-- name: UpsertParentSettings :one
INSERT INTO parent_settings (
    parent_id, daily_limit_min, session_limit_min, rest_interval_min,
    require_parent_confirm, pace_mode, compare_children, updated_at)
VALUES (
    sqlc.arg(parent_id), sqlc.arg(daily_limit_min), sqlc.arg(session_limit_min),
    sqlc.arg(rest_interval_min), sqlc.arg(require_parent_confirm), sqlc.arg(pace_mode),
    sqlc.arg(compare_children), now())
ON CONFLICT (parent_id) DO UPDATE SET
    daily_limit_min        = EXCLUDED.daily_limit_min,
    session_limit_min      = EXCLUDED.session_limit_min,
    rest_interval_min      = EXCLUDED.rest_interval_min,
    require_parent_confirm = EXCLUDED.require_parent_confirm,
    pace_mode              = EXCLUDED.pace_mode,
    compare_children       = EXCLUDED.compare_children,
    updated_at             = now()
RETURNING parent_id, daily_limit_min, session_limit_min, rest_interval_min,
          require_parent_confirm, pace_mode, compare_children;
