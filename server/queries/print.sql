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
