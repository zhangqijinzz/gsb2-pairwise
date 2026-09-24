ALTER TABLE background_jobs ADD COLUMN last_activity_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE background_jobs ADD COLUMN owner_pid INTEGER NOT NULL DEFAULT 0;
UPDATE background_jobs SET last_activity_at = COALESCE(finished_at, started_at, created_at);
CREATE INDEX idx_annotation_job_latest ON background_jobs(task_id, created_at DESC);
