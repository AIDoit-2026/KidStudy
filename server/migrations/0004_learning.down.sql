-- 0004 回退：先清掉数学知识点与模板种子，再按依赖顺序删表。
DELETE FROM knowledge_points WHERE subject_code = 'math' AND kind = 'math_skill';
DELETE FROM math_templates WHERE code IN (
    'M1_ADD5', 'M1_SUB5', 'M2_ADD10', 'M2_SUB10', 'M3_ADD20', 'M3_SUB20', 'M3_CMP20',
    'M4_ADD100', 'M4_SUB100', 'M4_MULTABLE', 'M4_DIVTABLE', 'M5_MUL2X1', 'M5_MIXED', 'M5_WORD'
);

DROP TABLE IF EXISTS practice_assignments;
DROP TABLE IF EXISTS daily_stats;
DROP TABLE IF EXISTS wrong_book_entries;
DROP TABLE IF EXISTS answer_logs;
DROP TABLE IF EXISTS session_items;
DROP TABLE IF EXISTS learning_sessions;
DROP TABLE IF EXISTS mastery_records;
