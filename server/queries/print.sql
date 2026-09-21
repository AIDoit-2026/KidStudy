-- 打印中心的查询（§4.6）。
--
-- 约定：
--  1. payload 是自洽的数据快照：worker 渲染 PDF 时只依赖它 + template_code，
--     不再回查 content/mastery —— 否则模板改版或内容下线会让历史任务渲染不出原样。
--  2. 「无过滤」用哨兵值表示（child_id = 全零 uuid），与 content.sql 用 '' 表示无过滤一致，
--     避免 sqlc 在可空参数上的类型推断歧义。
--  3. worker 取任务用 FOR UPDATE SKIP LOCKED，多实例并行拉取不会重复渲染同一个 job。

-- ---------------------------------------------------------------- 建与读

-- name: CreatePrintJob :one
INSERT INTO print_jobs (parent_id, child_id, template_code, params, payload, page_count)
VALUES (
    sqlc.arg(parent_id),
    sqlc.arg(child_id),
    sqlc.arg(template_code),
    sqlc.arg(params)::jsonb,
    sqlc.arg(payload)::jsonb,
    sqlc.arg(page_count)
)
RETURNING id, parent_id, child_id, template_code, params, payload, page_count,
          pdf_path, status, error_message, marked_done_at, created_at, updated_at;

-- name: GetPrintJob :one
SELECT id, parent_id, child_id, template_code, params, payload, page_count,
       pdf_path, status, error_message, marked_done_at, created_at, updated_at
FROM print_jobs
WHERE id = sqlc.arg(id) AND parent_id = sqlc.arg(parent_id);

-- name: ListPrintJobs :many
SELECT id, parent_id, child_id, template_code, params, payload, page_count,
       pdf_path, status, error_message, marked_done_at, created_at, updated_at
FROM print_jobs
WHERE parent_id = sqlc.arg(parent_id)
  AND (sqlc.arg(child_id)::uuid = '00000000-0000-0000-0000-000000000000'::uuid
       OR child_id = sqlc.arg(child_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountPrintJobs :one
SELECT count(*)::bigint AS total
FROM print_jobs
WHERE parent_id = sqlc.arg(parent_id)
  AND (sqlc.arg(child_id)::uuid = '00000000-0000-0000-0000-000000000000'::uuid
       OR child_id = sqlc.arg(child_id)::uuid);

-- ---------------------------------------------------------------- 状态机

-- 排队渲染：created / failed 才允许重新排队；ready 重复调用不改变状态（幂等）。
-- name: MarkPrintJobQueued :one
UPDATE print_jobs SET
    status        = CASE WHEN status IN ('created', 'failed') THEN 'queued' ELSE status END,
    error_message = CASE WHEN status IN ('created', 'failed') THEN '' ELSE error_message END,
    pdf_path      = CASE WHEN status = 'failed' THEN NULL ELSE pdf_path END,
    updated_at    = now()
WHERE id = sqlc.arg(id) AND parent_id = sqlc.arg(parent_id)
RETURNING id, status, pdf_path, page_count, error_message;

-- worker 拉取待渲染任务（多实例安全）。
-- name: ClaimPrintJobs :many
WITH claimed AS (
    SELECT id FROM print_jobs
    WHERE status = 'queued'
    ORDER BY created_at, id
    LIMIT sqlc.arg(lim)
    FOR UPDATE SKIP LOCKED
)
UPDATE print_jobs p SET status = 'rendering', updated_at = now()
FROM claimed c
WHERE p.id = c.id
RETURNING p.id, p.child_id, p.template_code, p.payload, p.page_count;

-- name: MarkPrintJobReady :one
UPDATE print_jobs SET
    status     = 'ready',
    pdf_path   = sqlc.arg(pdf_path),
    page_count = sqlc.arg(page_count),
    error_message = '',
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING id, status, pdf_path, page_count;

-- name: MarkPrintJobFailed :exec
UPDATE print_jobs SET
    status        = 'failed',
    error_message = sqlc.arg(error_message),
    updated_at    = now()
WHERE id = sqlc.arg(id);

-- 渲染中的任务超过 staleSec 视为进程崩溃遗留，退回 queued 重试（幂等：worker 启动时跑一次）。
-- name: RequeueStaleRendering :exec
UPDATE print_jobs SET status = 'queued', updated_at = now()
WHERE status = 'rendering' AND updated_at < now() - make_interval(secs => sqlc.arg(stale_sec)::double precision);

-- 纸质补录标记：只在还没标过的时候写，返回标记时间；已标过返回 0 行，service 据此判幂等。
-- name: MarkPrintJobDone :one
UPDATE print_jobs SET marked_done_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND parent_id = sqlc.arg(parent_id) AND marked_done_at IS NULL
RETURNING id, child_id, template_code, payload, marked_done_at;

-- 补录产生的学习会话：completed_by='parent'，并回指到 print_job，报表可区分来源（§4.11）。
-- name: InsertPrintSession :one
INSERT INTO learning_sessions (
    child_id, device_type, started_at, ended_at, question_count, answered_count,
    correct_count, star_count, completed_by, parent_note, status, print_job_id
)
VALUES (
    sqlc.arg(child_id), 'print', now(), now(),
    sqlc.arg(question_count), sqlc.arg(answered_count), sqlc.arg(correct_count),
    0, 'parent', sqlc.arg(parent_note), 'finished', sqlc.arg(print_job_id)
)
RETURNING id;

-- ---------------------------------------------------------------- 取数（渲染题面用）

-- 归属校验：parent_id 一起过滤，越权当作不存在（与 children 模块的 404 约定一致）。
-- name: PrintChildBasic :one
SELECT id, nickname, stage_code
FROM children
WHERE id = sqlc.arg(id) AND parent_id = sqlc.arg(parent_id) AND active;

-- 范围一：按阶段取（家长手动挑「S2 的字」这类）
-- name: PrintKPsByStage :many
SELECT id, subject_code, kind, name, difficulty
FROM knowledge_points
WHERE subject_code = sqlc.arg(subject_code)
  AND (sqlc.arg(stage_code)::text = '' OR stage_code = sqlc.arg(stage_code)::text)
  AND status = 'published'
ORDER BY difficulty, code
LIMIT sqlc.arg(lim);

-- 范围二：近期新学（默认「本周」= 近 7 天）—— 时间点由 Go 侧算好传进来，
-- 不在 SQL 里做 now() - interval 的推算，免得 sqlc 对 interval 参数推断出意外类型。
-- name: PrintKPsRecent :many
SELECT DISTINCT kp_id
FROM mastery_records
WHERE child_id = sqlc.arg(child_id)
  AND COALESCE(mastered_at, first_learned_at) IS NOT NULL
  AND COALESCE(mastered_at, first_learned_at) >= sqlc.arg(since)
ORDER BY kp_id
LIMIT sqlc.arg(lim);

-- 范围三：错题本（未移出的）
-- name: PrintKPsWrongBook :many
SELECT kp_id
FROM wrong_book_entries
WHERE child_id = sqlc.arg(child_id) AND cleared_at IS NULL
ORDER BY added_at DESC, kp_id
LIMIT sqlc.arg(lim);

-- 范围四：已掌握（做闪卡复习用）
-- name: PrintKPsMastered :many
SELECT kp_id
FROM mastery_records
WHERE child_id = sqlc.arg(child_id) AND level >= sqlc.arg(min_level)
ORDER BY COALESCE(mastered_at, first_learned_at) DESC NULLS LAST, kp_id
LIMIT sqlc.arg(lim);

-- 故事小册子：指定故事，或按级别挑一篇最短的（篇幅短的更适合一次朗读完）。
-- 只取已发布且通过适宜性筛查的，与孩子端的可见口径保持一致。
-- name: PrintGetStory :one
SELECT id, title, lang, level_code, body_md, questions, discussion, char_count
FROM stories
WHERE id = sqlc.arg(id) AND status = 'published' AND suitable;

-- name: PrintPickStory :one
SELECT id, title, lang, level_code, body_md, questions, discussion, char_count
FROM stories
WHERE status = 'published' AND suitable
  AND (sqlc.arg(stage_code)::text = '' OR level_code = sqlc.arg(stage_code)::text)
ORDER BY char_count, id
LIMIT 1;

-- 数学题的补录挂点：一道口算题卡属于哪个知识点。数学题的 kp 是「题型档」不是单题，
-- 所以整张卷子补录只推进这一个技能点（§4.6 步骤 6）。用 metadata 里的 template_code
-- 关联，与 M3 播种时的写入口径一致。
-- name: PrintMathKPByTemplate :one
SELECT id
FROM knowledge_points
WHERE kind = 'math_skill' AND metadata->>'template_code' = sqlc.arg(template_code)::text
LIMIT 1;

-- ---------------------------------------------------------------- 保留期清理

-- 超过保留期、PDF 还在的任务。payload 是不可变快照，清掉 PDF 后随时能重新渲染，
-- 所以清理只删文件、不动打印记录本身（家长还能看到「当时印过什么」）。
-- name: ListExpiredPrintJobs :many
SELECT id, pdf_path
FROM print_jobs
WHERE pdf_path IS NOT NULL AND created_at < sqlc.arg(before)
ORDER BY created_at
LIMIT sqlc.arg(lim);

-- name: ClearPrintJobPDF :exec
UPDATE print_jobs SET
    pdf_path      = NULL,
    page_count    = 0,
    status        = 'created',
    error_message = '',
    updated_at    = now()
WHERE id = sqlc.arg(id);
