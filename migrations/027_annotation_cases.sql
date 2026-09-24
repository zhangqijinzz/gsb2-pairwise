CREATE TABLE IF NOT EXISTS annotation_cases (
    task_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL DEFAULT '',
    container_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_annotation_cases_project
    ON annotation_cases(project_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_annotation_cases_container_unique
    ON annotation_cases(container_id)
    WHERE container_id <> '';
