-- 0005 回退：先撤 daily_stats 的两列，再删成就表（child_badges 依赖 badges）。
ALTER TABLE daily_stats
    DROP COLUMN IF EXISTS parent_confirmed,
    DROP COLUMN IF EXISTS passed;

DROP TABLE IF EXISTS child_badges;
DROP TABLE IF EXISTS badges;
