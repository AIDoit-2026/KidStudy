-- 回退 0003_content：内容域全量表 + curriculum_plan，按依赖倒序删除。

DROP TABLE IF EXISTS curriculum_plan;
DROP TABLE IF EXISTS content_review;
DROP TABLE IF EXISTS math_templates;
DROP TABLE IF EXISTS videos;
DROP TABLE IF EXISTS story_pairs;
DROP TABLE IF EXISTS stories;
DROP TABLE IF EXISTS en_sentences;
DROP TABLE IF EXISTS en_words;
DROP TABLE IF EXISTS hanzi_words;
DROP TABLE IF EXISTS hanzi;
DROP TABLE IF EXISTS knowledge_points;
