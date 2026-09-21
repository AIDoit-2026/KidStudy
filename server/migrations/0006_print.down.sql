-- 0006 回退：拆掉打印域
--
-- 顺序与 up 相反：先摘 learning_sessions 的外键列，再删 print_jobs。
-- pdf_path 指向的文件在 STORAGE_DIR 下，属运行期数据，迁移不回删（由运维清理）。

ALTER TABLE learning_sessions
    DROP COLUMN IF EXISTS print_job_id;

DROP INDEX IF EXISTS idx_print_jobs_pending;
DROP INDEX IF EXISTS idx_print_jobs_child;
DROP INDEX IF EXISTS idx_print_jobs_parent;

DROP TABLE IF EXISTS print_jobs;
