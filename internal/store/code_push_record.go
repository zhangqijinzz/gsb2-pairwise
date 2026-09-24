package store

import "database/sql"

type CodePushRecord struct {
	ID           string `json:"id"`
	TaskID       string `json:"taskId"`
	ModelRunID   string `json:"modelRunId"`
	SessionID    string `json:"sessionId"`
	SessionIndex int    `json:"sessionIndex"`
	LocalPath    string `json:"localPath"`
	RepoName     string `json:"repoName"`
	RepoURL      string `json:"repoUrl"`
	CommitSHA    string `json:"commitSha"`
	CommitURL    string `json:"commitUrl"`
	Branch       string `json:"branch"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage"`
	PushedAt     *int64 `json:"pushedAt"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

const codePushRecordColumns = `id, task_id, model_run_id, session_id, session_index,
	local_path, repo_name, repo_url, commit_sha, commit_url, branch, status,
	error_message, pushed_at, created_at, updated_at`

func (s *Store) ListCodePushRecords(taskID string) ([]CodePushRecord, error) {
	rows, err := s.DB.Query(
		`SELECT `+codePushRecordColumns+`
		   FROM code_push_records
		  WHERE task_id = ?
		  ORDER BY updated_at DESC, created_at DESC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]CodePushRecord, 0)
	for rows.Next() {
		record, err := scanCodePushRecordRows(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) FindCodePushRecordForReview(taskID, modelRunID string, sessionIndex int) (*CodePushRecord, error) {
	rows, err := s.DB.Query(
		`SELECT `+codePushRecordColumns+`
		   FROM code_push_records
		  WHERE task_id = ?
		    AND model_run_id = ?
		    AND session_index = ?
		    AND commit_sha <> ''
		  ORDER BY updated_at DESC, created_at DESC
		  LIMIT 1`,
		taskID, modelRunID, sessionIndex,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		record, err := scanCodePushRecordRows(rows)
		if err != nil {
			return nil, err
		}
		return &record, rows.Err()
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	record, err := scanCodePushRecord(s.DB.QueryRow(
		`SELECT `+codePushRecordColumns+`
		   FROM code_push_records
		  WHERE task_id = ?
		    AND model_run_id = ?
		    AND commit_sha <> ''
		  ORDER BY session_index DESC, updated_at DESC, created_at DESC
		  LIMIT 1`,
		taskID, modelRunID,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) GetCodePushRecord(id string) (*CodePushRecord, error) {
	record, err := scanCodePushRecord(s.DB.QueryRow(
		`SELECT `+codePushRecordColumns+` FROM code_push_records WHERE id = ?`,
		id,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) UpsertCodePushRecord(record CodePushRecord) error {
	_, err := s.DB.Exec(
		`INSERT INTO code_push_records (
			id, task_id, model_run_id, session_id, session_index, local_path,
			repo_name, repo_url, commit_sha, commit_url, branch, status,
			error_message, pushed_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			task_id = excluded.task_id,
			model_run_id = excluded.model_run_id,
			session_id = excluded.session_id,
			session_index = excluded.session_index,
			local_path = excluded.local_path,
			repo_name = excluded.repo_name,
			repo_url = excluded.repo_url,
			commit_sha = excluded.commit_sha,
			commit_url = excluded.commit_url,
			branch = excluded.branch,
			status = excluded.status,
			error_message = excluded.error_message,
			pushed_at = excluded.pushed_at,
			updated_at = strftime('%s','now')`,
		record.ID, record.TaskID, record.ModelRunID, record.SessionID, record.SessionIndex,
		record.LocalPath, record.RepoName, record.RepoURL, record.CommitSHA, record.CommitURL,
		record.Branch, record.Status, record.ErrorMessage, record.PushedAt,
	)
	return err
}

func scanCodePushRecord(row interface {
	Scan(dest ...interface{}) error
}) (CodePushRecord, error) {
	var record CodePushRecord
	err := row.Scan(
		&record.ID,
		&record.TaskID,
		&record.ModelRunID,
		&record.SessionID,
		&record.SessionIndex,
		&record.LocalPath,
		&record.RepoName,
		&record.RepoURL,
		&record.CommitSHA,
		&record.CommitURL,
		&record.Branch,
		&record.Status,
		&record.ErrorMessage,
		&record.PushedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	return record, err
}

func scanCodePushRecordRows(rows *sql.Rows) (CodePushRecord, error) {
	return scanCodePushRecord(rows)
}
