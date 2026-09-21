-- 0006 打印域：打印任务与纸质补录
--
-- 设计依据：《开发设计文档》§3.3（print_jobs）、§4.6 打印中心、§7 M5。
--
-- 两处对设计的补充（有意为之）：
--  1. status 拆出 queued / rendering 两态。设计只写了「PDF 排队」，但排队语义需要一个
--     可被 worker 拉取的中间态：POST .../pdf 只把 created 推到 queued 就返回 202，
--     真正的渲染在 worker 里跑（§8「不在请求处理器里跑长任务」）。
--  2. payload 里额外存 template_code 与 render_version。模板改版后，历史任务的
--     data/PDF 必须仍按当时的数据渲染，不能跟着模板一起漂。

CREATE TABLE print_jobs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id      uuid        NOT NULL REFERENCES parents(id) ON DELETE CASCADE,
    child_id       uuid        NOT NULL REFERENCES children(id) ON DELETE CASCADE,
    template_code  text        NOT NULL,
    params         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    payload        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    page_count     int         NOT NULL DEFAULT 0,
    pdf_path       text,
    status         text        NOT NULL DEFAULT 'created',
    error_message  text        NOT NULL DEFAULT '',
    marked_done_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT print_status_valid    CHECK (status IN ('created', 'queued', 'rendering', 'ready', 'failed')),
    CONSTRAINT print_page_count_ok   CHECK (page_count >= 0),
    CONSTRAINT print_template_code_nn CHECK (length(template_code) > 0)
);

COMMENT ON TABLE print_jobs IS '打印记录：一次「选模板 + 填参数」= 一个 job；payload 是渲染数据快照（§4.6）';
COMMENT ON COLUMN print_jobs.params IS '家长填的原始参数（范围 / 每页题量 / 字号 / 是否带拼音 / 是否含答案 / 纸张 / 份数等）';
COMMENT ON COLUMN print_jobs.payload IS '生成好的题面数据快照：{template_code, render_version, title, meta, pages:[...], answer_pages:[...]}；答案页只在家长版渲染';
COMMENT ON COLUMN print_jobs.status IS 'created=建好未排 / queued=已排待渲染 / rendering=worker 渲染中 / ready=PDF 可下载 / failed=渲染失败（见 error_message）';
COMMENT ON COLUMN print_jobs.pdf_path IS '相对 STORAGE_DIR 的 PDF 路径，如 print/{id}.pdf；为空表示尚未生成';
COMMENT ON COLUMN print_jobs.marked_done_at IS '家长标记「纸上已做完」的时间；非空表示这次打印已计入进度（补录幂等靠它）';

CREATE INDEX idx_print_jobs_parent  ON print_jobs (parent_id, created_at DESC);
CREATE INDEX idx_print_jobs_child   ON print_jobs (child_id, created_at DESC);
-- worker 拉取待渲染任务用：只索引排队与渲染中的行，避免全表扫描
CREATE INDEX idx_print_jobs_pending ON print_jobs (created_at)
    WHERE status IN ('queued', 'rendering');

-- 补录溯源：标记完成时写进 learning_sessions 的会话要能回指到是哪张打印纸。
ALTER TABLE learning_sessions
    ADD COLUMN IF NOT EXISTS print_job_id uuid REFERENCES print_jobs(id) ON DELETE SET NULL;

COMMENT ON COLUMN learning_sessions.print_job_id IS '纸质补录来源：非空表示这次会话由 print_jobs 的 mark-done 补录产生（§4.6 步骤 6）';
