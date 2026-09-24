package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/pinru/internal/annotation"
)

var (
	ErrAnnotationRevisionConflict    = errors.New("annotation case revision conflict")
	ErrAnnotationInitialSHAImmutable = errors.New("annotation case initial SHA is immutable after a round exists")
	ErrAnnotationContainerInUse      = errors.New("annotation container is already bound to another task")
)

func (s *Store) GetAnnotationCase(taskID string) (*annotation.Case, error) {
	var payload string
	var revision int
	var updatedAt int64
	err := s.DB.QueryRow(
		`SELECT payload_json, revision, updated_at
		   FROM annotation_cases
		  WHERE task_id = ?`,
		strings.TrimSpace(taskID),
	).Scan(&payload, &revision, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	annotationCase, err := decodeAnnotationCase(payload, revision, updatedAt)
	if err != nil {
		return nil, err
	}
	return &annotationCase, nil
}

// ListAnnotationCases returns every case when projectID is empty.
func (s *Store) ListAnnotationCases(projectID string) ([]annotation.Case, error) {
	query := `SELECT payload_json, revision, updated_at FROM annotation_cases`
	args := make([]any, 0, 1)
	if strings.TrimSpace(projectID) != "" {
		query += ` WHERE project_id = ?`
		args = append(args, strings.TrimSpace(projectID))
	}
	query += ` ORDER BY updated_at DESC, task_id ASC`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cases := make([]annotation.Case, 0)
	for rows.Next() {
		var payload string
		var revision int
		var updatedAt int64
		if err := rows.Scan(&payload, &revision, &updatedAt); err != nil {
			return nil, err
		}
		annotationCase, err := decodeAnnotationCase(payload, revision, updatedAt)
		if err != nil {
			return nil, err
		}
		cases = append(cases, annotationCase)
	}
	return cases, rows.Err()
}

// SaveAnnotationCase inserts at expectedRevision 0 and otherwise performs a
// compare-and-swap update. Revision and UpdatedAt are assigned by the store.
func (s *Store) SaveAnnotationCase(input annotation.Case, expectedRevision int) (*annotation.Case, error) {
	// Independent reviews may finish together. SQLite still has one writer, so
	// serialize the short compare-and-swap transaction without serializing the
	// long-running model calls that precede it.
	s.annotationMu.Lock()
	defer s.annotationMu.Unlock()
	input.TaskID = strings.TrimSpace(input.TaskID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.ContainerID = strings.TrimSpace(input.ContainerID)
	normalizeAnnotationCaseSlices(&input)
	if input.TaskID == "" {
		return nil, fmt.Errorf("annotation taskId is required")
	}
	if expectedRevision < 0 {
		return nil, fmt.Errorf("%w: expected revision must be non-negative", ErrAnnotationRevisionConflict)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var previousPayload string
	var actualRevision int
	lookupErr := tx.QueryRow(
		`SELECT payload_json, revision FROM annotation_cases WHERE task_id = ?`,
		input.TaskID,
	).Scan(&previousPayload, &actualRevision)
	if lookupErr != nil && lookupErr != sql.ErrNoRows {
		return nil, lookupErr
	}
	exists := lookupErr == nil
	if expectedRevision == 0 && exists {
		return nil, fmt.Errorf("%w: task %s is already at revision %d", ErrAnnotationRevisionConflict, input.TaskID, actualRevision)
	}
	if expectedRevision > 0 && (!exists || actualRevision != expectedRevision) {
		return nil, fmt.Errorf("%w: task %s expected %d, found %d", ErrAnnotationRevisionConflict, input.TaskID, expectedRevision, actualRevision)
	}
	if exists {
		previous, err := decodeAnnotationCase(previousPayload, actualRevision, 0)
		if err != nil {
			return nil, err
		}
		if (len(previous.Rounds) > 0 || pairwiseHasEvidence(previous)) && previous.InitialSHA != input.InitialSHA {
			return nil, fmt.Errorf("%w: task %s", ErrAnnotationInitialSHAImmutable, input.TaskID)
		}
	}
	if input.ContainerID != "" {
		var otherTaskID string
		err := tx.QueryRow(
			`SELECT task_id FROM annotation_cases WHERE container_id = ? AND task_id <> ? LIMIT 1`,
			input.ContainerID, input.TaskID,
		).Scan(&otherTaskID)
		if err == nil {
			return nil, fmt.Errorf("%w: container %s is bound to task %s", ErrAnnotationContainerInUse, input.ContainerID, otherTaskID)
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}

	now := time.Now().Unix()
	input.Revision = expectedRevision + 1
	input.UpdatedAt = now
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if expectedRevision == 0 {
		result, err := tx.Exec(
			`INSERT INTO annotation_cases
				(task_id, project_id, container_id, payload_json, revision, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(task_id) DO NOTHING`,
			input.TaskID, input.ProjectID, input.ContainerID, string(payload), input.Revision, now,
		)
		if err != nil {
			return nil, mapAnnotationWriteError(err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, fmt.Errorf("%w: task %s was inserted concurrently", ErrAnnotationRevisionConflict, input.TaskID)
		}
	} else {
		result, err := tx.Exec(
			`UPDATE annotation_cases
			    SET project_id = ?, container_id = ?, payload_json = ?, revision = ?, updated_at = ?
			  WHERE task_id = ? AND revision = ?`,
			input.ProjectID, input.ContainerID, string(payload), input.Revision, now,
			input.TaskID, expectedRevision,
		)
		if err != nil {
			return nil, mapAnnotationWriteError(err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, fmt.Errorf("%w: task %s changed while saving", ErrAnnotationRevisionConflict, input.TaskID)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, mapAnnotationWriteError(err)
	}
	committed = true
	return &input, nil
}

func decodeAnnotationCase(payload string, revision int, updatedAt int64) (annotation.Case, error) {
	var annotationCase annotation.Case
	if err := json.Unmarshal([]byte(payload), &annotationCase); err != nil {
		return annotation.Case{}, fmt.Errorf("decode annotation case: %w", err)
	}
	normalizeAnnotationCaseSlices(&annotationCase)
	annotationCase.Revision = revision
	if updatedAt != 0 {
		annotationCase.UpdatedAt = updatedAt
	}
	return annotationCase, nil
}

func normalizeAnnotationCaseSlices(annotationCase *annotation.Case) {
	annotation.NormalizeCase(annotationCase)
}

func pairwiseHasEvidence(c annotation.Case) bool {
	if c.Pairwise == nil {
		return false
	}
	return c.Pairwise.RunA.SessionID != "" || c.Pairwise.RunB.SessionID != "" ||
		c.Pairwise.RunA.CaptureID != "" || c.Pairwise.RunB.CaptureID != "" ||
		c.Pairwise.RunA.DeliverableSHA != "" || c.Pairwise.RunB.DeliverableSHA != ""
}

func mapAnnotationWriteError(err error) error {
	message := err.Error()
	if strings.Contains(message, "annotation_cases.container_id") || strings.Contains(message, "idx_annotation_cases_container_unique") {
		return fmt.Errorf("%w: %v", ErrAnnotationContainerInUse, err)
	}
	if strings.Contains(strings.ToLower(message), "database is locked") || strings.Contains(strings.ToLower(message), "busy") {
		return fmt.Errorf("%w: %v", ErrAnnotationRevisionConflict, err)
	}
	return err
}
