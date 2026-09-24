CREATE TABLE IF NOT EXISTS code_push_records (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    model_run_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    session_index INTEGER NOT NULL DEFAULT 0,
    local_path TEXT NOT NULL,
    repo_name TEXT NOT NULL,
    repo_url TEXT NOT NULL DEFAULT '',
    commit_sha TEXT NOT NULL DEFAULT '',
    commit_url TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT 'main',
    status TEXT NOT NULL DEFAULT 'committed',
    error_message TEXT NOT NULL DEFAULT '',
    pushed_at INTEGER,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
    FOREIGN KEY (model_run_id) REFERENCES model_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_code_push_records_task ON code_push_records(task_id);
CREATE INDEX IF NOT EXISTS idx_code_push_records_model_run ON code_push_records(model_run_id);
CREATE INDEX IF NOT EXISTS idx_code_push_records_session ON code_push_records(session_id);
