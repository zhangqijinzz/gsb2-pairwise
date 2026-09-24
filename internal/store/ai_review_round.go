package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/blueship581/pinru/internal/errs"
)

type AiReviewRound struct {
	ID                     string  `json:"id"`
	TaskID                 string  `json:"taskId"`
	ModelRunID             *string `json:"modelRunId"`
	LocalPath              string  `json:"localPath"`
	ModelName              string  `json:"modelName"`
	RoundNumber            int     `json:"roundNumber"`
	OriginalPrompt         string  `json:"originalPrompt"`
	PromptText             string  `json:"promptText"`
	PromptDifficulty       string  `json:"promptDifficulty"`
	Status                 string  `json:"status"`
	IsCompleted            *bool   `json:"isCompleted"`
	IsSatisfied            *bool   `json:"isSatisfied"`
	ReviewNotes            string  `json:"reviewNotes"`
	DissatisfactionSummary string  `json:"dissatisfactionSummary"`
	NextPrompt             string  `json:"nextPrompt"`
	NextPromptTaskType     string  `json:"nextPromptTaskType"`
	ProjectType            string  `json:"projectType"`
	ChangeScope            string  `json:"changeScope"`
	KeyLocations           string  `json:"keyLocations"`
	JobID                  *string `json:"jobId"`
	CreatedAt              int64   `json:"createdAt"`
	UpdatedAt              int64   `json:"updatedAt"`
}

const aiReviewRoundColumns = `id, task_id, model_run_id, local_path, model_name,
	round_number, original_prompt, prompt_text,
	prompt_difficulty, status, is_completed, is_satisfied, review_notes, dissatisfaction_summary, next_prompt,
	next_prompt_task_type, project_type, change_scope, key_locations, job_id,
	created_at, updated_at`

func (s *Store) CreateAiReviewRound(round AiReviewRound) error {
	_, err := s.DB.Exec(
		`INSERT INTO ai_review_rounds (
			id, task_id, model_run_id, local_path, model_name,
			round_number, original_prompt, prompt_text,
			prompt_difficulty, status, is_completed, is_satisfied, review_notes, dissatisfaction_summary, next_prompt,
			next_prompt_task_type, project_type, change_scope, key_locations, job_id
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		round.ID, round.TaskID, round.ModelRunID, round.LocalPath, round.ModelName,
		round.RoundNumber, round.OriginalPrompt, round.PromptText,
		normalizePromptDifficulty(round.PromptDifficulty),
		round.Status, boolPtrToNullableInt(round.IsCompleted), boolPtrToNullableInt(round.IsSatisfied),
		round.ReviewNotes, round.DissatisfactionSummary, round.NextPrompt,
		normalizeAiReviewTaskType(round.NextPromptTaskType),
		round.ProjectType, round.ChangeScope, round.KeyLocations, round.JobID,
	)
	return err
}

func (s *Store) FinalizeAiReviewRound(id, status string, isCompleted, isSatisfied *bool, reviewNotes, dissatisfactionSummary, nextPrompt, nextPromptTaskType, projectType, changeScope, keyLocations string) error {
	res, err := s.DB.Exec(
		`UPDATE ai_review_rounds
		    SET status = ?, is_completed = ?, is_satisfied = ?,
		        review_notes = ?, dissatisfaction_summary = ?, next_prompt = ?, next_prompt_task_type = ?,
		        project_type = ?, change_scope = ?, key_locations = ?,
		        updated_at = strftime('%s','now')
		  WHERE id = ?`,
		status, boolPtrToNullableInt(isCompleted), boolPtrToNullableInt(isSatisfied),
		reviewNotes, dissatisfactionSummary, nextPrompt,
		normalizeAiReviewTaskType(nextPromptTaskType),
		projectType, changeScope, keyLocations,
		id,
	)
	return ensureRowsAffected(res, err, errs.FmtStoreReviewRoundNotFound, id)
}

func (s *Store) UpdateAiReviewRoundStatus(id, status string, jobID *string) error {
	res, err := s.DB.Exec(
		`UPDATE ai_review_rounds SET status = ?, job_id = ?, updated_at = strftime('%s','now') WHERE id = ?`,
		status, jobID, id,
	)
	return ensureRowsAffected(res, err, errs.FmtStoreReviewRoundNotFound, id)
}

func (s *Store) UpdateAiReviewRoundNotes(id, reviewNotes, nextPrompt, nextPromptTaskType string) error {
	res, err := s.DB.Exec(
		`UPDATE ai_review_rounds SET review_notes = ?, next_prompt = ?, next_prompt_task_type = ?, updated_at = strftime('%s','now') WHERE id = ?`,
		reviewNotes, nextPrompt, normalizeAiReviewTaskType(nextPromptTaskType), id,
	)
	return ensureRowsAffected(res, err, errs.FmtStoreReviewRoundNotFound, id)
}

func (s *Store) UpdateAiReviewRoundDissatisfactionSummary(id, dissatisfactionSummary string) error {
	res, err := s.DB.Exec(
		`UPDATE ai_review_rounds SET dissatisfaction_summary = ?, updated_at = strftime('%s','now') WHERE id = ?`,
		strings.TrimSpace(dissatisfactionSummary), id,
	)
	return ensureRowsAffected(res, err, errs.FmtStoreReviewRoundNotFound, id)
}

func (s *Store) DeleteAiReviewRound(id string) (*AiReviewRound, error) {
	id = strings.TrimSpace(id)
	round, err := s.GetAiReviewRound(id)
	if err != nil {
		return nil, err
	}
	if round == nil {
		return nil, fmt.Errorf(errs.FmtStoreReviewRoundNotFound, id)
	}
	if round.Status == "running" {
		return nil, fmt.Errorf("复审轮次正在运行，请先取消或等待完成后再重置")
	}
	res, err := s.DB.Exec(`DELETE FROM ai_review_rounds WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if err := ensureRowsAffected(res, nil, errs.FmtStoreReviewRoundNotFound, id); err != nil {
		return nil, err
	}
	return round, nil
}

func (s *Store) GetAiReviewRound(id string) (*AiReviewRound, error) {
	row := s.DB.QueryRow(
		`SELECT `+aiReviewRoundColumns+` FROM ai_review_rounds WHERE id = ?`, id,
	)
	round, err := scanAiReviewRound(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &round, nil
}

func (s *Store) GetLatestAiReviewRound(modelRunID *string, localPath string) (*AiReviewRound, error) {
	var row *sql.Row
	if modelRunID != nil && strings.TrimSpace(*modelRunID) != "" {
		row = s.DB.QueryRow(
			`SELECT `+aiReviewRoundColumns+` FROM ai_review_rounds
			  WHERE model_run_id = ? ORDER BY round_number DESC LIMIT 1`,
			strings.TrimSpace(*modelRunID),
		)
	} else {
		row = s.DB.QueryRow(
			`SELECT `+aiReviewRoundColumns+` FROM ai_review_rounds
			  WHERE model_run_id IS NULL AND local_path = ? ORDER BY round_number DESC LIMIT 1`,
			strings.TrimSpace(localPath),
		)
	}
	round, err := scanAiReviewRound(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &round, nil
}

func (s *Store) ListAiReviewRoundsByModelRun(modelRunID string) ([]AiReviewRound, error) {
	rows, err := s.DB.Query(
		`SELECT `+aiReviewRoundColumns+` FROM ai_review_rounds
		  WHERE model_run_id = ? ORDER BY round_number ASC`,
		modelRunID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAiReviewRounds(rows)
}

func (s *Store) ListAiReviewRoundsByTask(taskID string) ([]AiReviewRound, error) {
	rows, err := s.DB.Query(
		`SELECT `+aiReviewRoundColumns+` FROM ai_review_rounds
		  WHERE task_id = ? ORDER BY model_run_id, round_number ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAiReviewRounds(rows)
}

func (s *Store) GetNextRoundNumber(modelRunID *string, localPath string) (int, error) {
	var maxRound sql.NullInt64
	if modelRunID != nil && strings.TrimSpace(*modelRunID) != "" {
		err := s.DB.QueryRow(
			`SELECT MAX(round_number) FROM ai_review_rounds WHERE model_run_id = ?`,
			strings.TrimSpace(*modelRunID),
		).Scan(&maxRound)
		if err != nil {
			return 1, err
		}
	} else {
		err := s.DB.QueryRow(
			`SELECT MAX(round_number) FROM ai_review_rounds WHERE model_run_id IS NULL AND local_path = ?`,
			strings.TrimSpace(localPath),
		).Scan(&maxRound)
		if err != nil {
			return 1, err
		}
	}
	if !maxRound.Valid {
		return 1, nil
	}
	return int(maxRound.Int64) + 1, nil
}

func SummarizeAiReviewRounds(rounds []AiReviewRound) (status string, round int, notes *string) {
	if len(rounds) == 0 {
		return "none", 0, nil
	}

	// If the latest round is running, report that immediately.
	latest := rounds[len(rounds)-1]
	if latest.Status == "running" {
		return "running", latest.RoundNumber, nil
	}

	// Find the most recent round with a meaningful status (pass/warning),
	// skipping rounds in 'none' (e.g. reset after cancellation).
	for i := len(rounds) - 1; i >= 0; i-- {
		r := rounds[i]
		switch r.Status {
		case "pass":
			return "pass", r.RoundNumber, nil
		case "warning":
			text := strings.TrimSpace(r.ReviewNotes)
			if text != "" {
				return "warning", r.RoundNumber, &text
			}
			return "warning", r.RoundNumber, nil
		}
	}

	return "none", 0, nil
}

func collectAiReviewRounds(rows *sql.Rows) ([]AiReviewRound, error) {
	var result []AiReviewRound
	for rows.Next() {
		round, err := scanAiReviewRound(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, round)
	}
	return result, rows.Err()
}

func scanAiReviewRound(scanner interface {
	Scan(dest ...any) error
}) (AiReviewRound, error) {
	var (
		round          AiReviewRound
		isCompletedRaw sql.NullInt64
		isSatisfiedRaw sql.NullInt64
	)
	err := scanner.Scan(
		&round.ID, &round.TaskID, &round.ModelRunID, &round.LocalPath, &round.ModelName,
		&round.RoundNumber, &round.OriginalPrompt, &round.PromptText,
		&round.PromptDifficulty, &round.Status, &isCompletedRaw, &isSatisfiedRaw, &round.ReviewNotes, &round.DissatisfactionSummary, &round.NextPrompt,
		&round.NextPromptTaskType,
		&round.ProjectType, &round.ChangeScope, &round.KeyLocations, &round.JobID,
		&round.CreatedAt, &round.UpdatedAt,
	)
	if err != nil {
		return AiReviewRound{}, err
	}
	round.PromptDifficulty = normalizePromptDifficulty(round.PromptDifficulty)
	round.NextPromptTaskType = normalizeAiReviewTaskType(round.NextPromptTaskType)
	round.IsCompleted = nullableIntToBoolPtr(isCompletedRaw)
	round.IsSatisfied = nullableIntToBoolPtr(isSatisfiedRaw)
	return round, nil
}

func normalizeAiReviewTaskType(taskType string) string {
	trimmed := strings.TrimSpace(taskType)
	if trimmed == "" {
		return defaultTaskType
	}
	switch strings.ToLower(strings.ReplaceAll(trimmed, " ", "")) {
	case "bugfix", "bug修复", "缺陷修复":
		return "Bug修复"
	case "feature", "feature迭代", "功能开发":
		return "Feature迭代"
	case "代码生成", "0-1代码生成", "0-1", "0到1", "从0到1", "从零到一":
		return "0-1代码生成"
	case "代码理解":
		return "代码理解"
	case "refactor", "代码重构":
		return "代码重构"
	case "工程化":
		return "工程化"
	case "test", "测试", "测试补全", "代码测试":
		return "代码测试"
	case "未分类", "未归类", "uncategorized", "unclassified":
		return defaultTaskType
	default:
		return trimmed
	}
}
