package store

import (
	"errors"
	"os"
	"runtime"
	"syscall"

	"github.com/blueship581/pinru/internal/annotation"
)

// A process restart can leave a job owned by a dead process in the database.
// Recover proven-dead owners immediately and retain the deadline fallback;
// absence of fresh model output alone is not a failure.
func (s *Store) RecoverExpiredAnnotationJobs() error {
	rows, err := s.DB.Query(`SELECT id, owner_pid FROM background_jobs
 WHERE status IN ('pending','running')
 AND job_type IN ('annotation_capture_table','annotation_batch_capture_table','annotation_review','annotation_resume')
 AND owner_pid>0`)
	if err != nil {
		return err
	}
	type ownedJob struct {
		id  string
		pid int
	}
	var candidates []ownedJob
	for rows.Next() {
		var candidate ownedJob
		if err := rows.Scan(&candidate.id, &candidate.pid); err != nil {
			_ = rows.Close()
			return err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if !processOwnerDead(candidate.pid) {
			continue
		}
		if _, err := s.DB.Exec(`UPDATE background_jobs SET status='error',
 error_message='任务所属进程已退出，请重试；已保存的轨迹与评分保留', finished_at=strftime('%s','now')
 WHERE id=? AND owner_pid=? AND status IN ('pending','running')`, candidate.id, candidate.pid); err != nil {
			return err
		}
	}
	_, err = s.DB.Exec(`UPDATE background_jobs SET status='error',
 error_message='已超过任务执行时限，请重试；已保存的轨迹与评分保留', finished_at=strftime('%s','now')
 WHERE status IN ('pending','running')
 AND job_type IN ('annotation_capture_table','annotation_batch_capture_table','annotation_review','annotation_resume')
 AND timeout_seconds>0
 AND ((status='pending' AND COALESCE(NULLIF(last_activity_at,0),created_at)+timeout_seconds < CAST(strftime('%s','now') AS INTEGER))
   OR (status='running' AND COALESCE(started_at,created_at)+timeout_seconds < CAST(strftime('%s','now') AS INTEGER)))`)
	return err
}

// processOwnerDead only returns true when this platform can prove the process
// no longer exists. Unknown owners and inconclusive errors stay active until
// the separate deadline recovery applies.
func processOwnerDead(pid int) bool {
	if pid <= 0 || pid == os.Getpid() || runtime.GOOS == "windows" {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer func() { _ = process.Release() }()
	err = process.Signal(syscall.Signal(0))
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

// Query project-scoped latest jobs without the background task drawer's 100-row limit.
func (s *Store) LatestAnnotationPreparations(projectID string) (map[string]*annotation.Preparation, error) {
	if err := s.RecoverExpiredAnnotationJobs(); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT j.task_id, j.id, j.status, j.progress,
 COALESCE(j.progress_message,''), COALESCE(j.error_message,''),
 COALESCE(j.started_at,j.created_at), COALESCE(j.finished_at,0), COALESCE(NULLIF(j.last_activity_at,0), j.started_at,j.created_at)
 FROM background_jobs j JOIN tasks t ON t.id=j.task_id
 WHERE t.project_config_id=? AND j.job_type IN ('annotation_capture_table','annotation_review','annotation_resume')
 ORDER BY CASE WHEN j.status IN ('pending','running') THEN 0 ELSE 1 END,
 COALESCE(j.started_at,NULLIF(j.last_activity_at,0),j.created_at) DESC, j.rowid DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]*annotation.Preparation{}
	for rows.Next() {
		var id string
		var p annotation.Preparation
		if err := rows.Scan(&id, &p.JobID, &p.Status, &p.Progress, &p.Message, &p.Error, &p.StartedAt, &p.FinishedAt, &p.LastActivityAt); err != nil {
			return nil, err
		}
		if _, ok := result[id]; !ok {
			result[id] = &p
		}
	}
	return result, rows.Err()
}
