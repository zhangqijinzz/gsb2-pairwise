ALTER TABLE tasks ADD COLUMN prompt_difficulty TEXT NOT NULL DEFAULT '一般';
ALTER TABLE ai_review_nodes ADD COLUMN prompt_difficulty TEXT NOT NULL DEFAULT '一般';
ALTER TABLE ai_review_rounds ADD COLUMN prompt_difficulty TEXT NOT NULL DEFAULT '一般';
