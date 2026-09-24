CREATE TABLE IF NOT EXISTS project_profiles (
    repo_path TEXT NOT NULL,
    commit_hash TEXT NOT NULL,
    profile_text TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    PRIMARY KEY (repo_path, commit_hash)
);
