package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/blueship581/pinru/migrations"
)

func openAnnotationProgressTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "pinru.db"), migrations.All()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCreateBackgroundJobRecordsCurrentOwner(t *testing.T) {
	st := openAnnotationProgressTestStore(t)
	if err := st.CreateBackgroundJob(BackgroundJob{ID: "owned", JobType: "annotation_resume", Status: "pending", InputPayload: "{}", CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	job, err := st.GetBackgroundJob("owned")
	if err != nil {
		t.Fatal(err)
	}
	if job.OwnerPID != os.Getpid() {
		t.Fatalf("owner pid = %d, want %d", job.OwnerPID, os.Getpid())
	}
}

func TestRecoverAnnotationJobsUsesDeadOwnerAndDeadlineConservatively(t *testing.T) {
	st := openAnnotationProgressTestStore(t)
	deadPID := exitedProcessPID(t)
	now := time.Now().Unix()
	for _, job := range []BackgroundJob{
		{ID: "dead-annotation", JobType: "annotation_resume", Status: "running", InputPayload: "{}", CreatedAt: now, TimeoutSeconds: 3600},
		{ID: "live-annotation", JobType: "annotation_review", Status: "running", InputPayload: "{}", CreatedAt: now, TimeoutSeconds: 3600},
		{ID: "unknown-owner", JobType: "annotation_capture_table", Status: "running", InputPayload: "{}", CreatedAt: now, TimeoutSeconds: 3600},
		{ID: "dead-other-job", JobType: "prompt_generate", Status: "running", InputPayload: "{}", CreatedAt: now, TimeoutSeconds: 3600},
		{ID: "expired-unknown", JobType: "annotation_resume", Status: "running", InputPayload: "{}", CreatedAt: 100, TimeoutSeconds: 1},
	} {
		if err := st.CreateBackgroundJob(job); err != nil {
			t.Fatal(err)
		}
	}
	for id, pid := range map[string]int{"dead-annotation": deadPID, "unknown-owner": 0, "dead-other-job": deadPID, "expired-unknown": 0} {
		if _, err := st.DB.Exec(`UPDATE background_jobs SET owner_pid=? WHERE id=?`, pid, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RecoverExpiredAnnotationJobs(); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"dead-annotation": "error",
		"live-annotation": "running",
		"unknown-owner":   "running",
		"dead-other-job":  "running",
		"expired-unknown": "error",
	} {
		job, err := st.GetBackgroundJob(id)
		if err != nil || job.Status != want {
			t.Fatalf("job %s = %+v, %v; want status %s", id, job, err, want)
		}
	}
	if err := st.IncrementBackgroundJobRetry("dead-annotation"); err != nil {
		t.Fatal(err)
	}
	retried, err := st.GetBackgroundJob("dead-annotation")
	if err != nil || retried.Status != "pending" || retried.OwnerPID != os.Getpid() {
		t.Fatalf("retried job = %+v, %v; want pending with current owner", retried, err)
	}
}

func TestTerminalBackgroundJobCannotBeRevivedByWorkerUpdates(t *testing.T) {
	st := openAnnotationProgressTestStore(t)
	for _, status := range []string{"done", "error", "cancelled"} {
		id := "terminal-" + status
		if err := st.CreateBackgroundJob(BackgroundJob{ID: id, JobType: "annotation_resume", Status: status, InputPayload: "{}", CreatedAt: time.Now().Unix()}); err != nil {
			t.Fatal(err)
		}
		if err := st.StartBackgroundJob(id); err != nil {
			t.Fatal(err)
		}
		if err := st.UpdateBackgroundJobProgress(id, 40, "late progress"); err != nil {
			t.Fatal(err)
		}
		if err := st.CompleteBackgroundJob(id, nil); err != nil {
			t.Fatal(err)
		}
		job, err := st.GetBackgroundJob(id)
		if err != nil || job.Status != status {
			t.Fatalf("job %s = %+v, %v; terminal state was revived", id, job, err)
		}
	}
}

func TestLatestAnnotationPreparationUsesMostRecentExecutionAttempt(t *testing.T) {
	st := openAnnotationProgressTestStore(t)
	project := Project{ID: "preparation-order-project", Name: "Preparation order", CloneBasePath: t.TempDir()}
	if err := st.CreateProject(project); err != nil {
		t.Fatal(err)
	}
	projectID := project.ID
	task := Task{ID: "preparation-order-task", ProjectName: "Preparation order task", ProjectConfigID: &projectID}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	taskID := task.ID
	for _, job := range []BackgroundJob{
		{ID: "attempt-a", JobType: "annotation_resume", TaskID: &taskID, Status: "error", InputPayload: "{}", MaxRetries: 2, CreatedAt: 100},
		{ID: "attempt-b", JobType: "annotation_review", TaskID: &taskID, Status: "done", InputPayload: "{}", CreatedAt: 200},
	} {
		if err := st.CreateBackgroundJob(job); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.IncrementBackgroundJobRetry("attempt-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.StartBackgroundJob("attempt-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteBackgroundJob("attempt-a", nil); err != nil {
		t.Fatal(err)
	}

	preparations, err := st.LatestAnnotationPreparations(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := preparations[task.ID]; got == nil || got.JobID != "attempt-a" || got.Status != "done" {
		t.Fatalf("latest preparation = %+v, want completed retry attempt-a", got)
	}
	if err := st.CreateBackgroundJob(BackgroundJob{ID: "active-attempt", JobType: "annotation_review", TaskID: &taskID, Status: "pending", InputPayload: "{}", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	preparations, err = st.LatestAnnotationPreparations(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := preparations[task.ID]; got == nil || got.JobID != "active-attempt" || got.Status != "pending" {
		t.Fatalf("latest preparation = %+v, want active job before completed attempts", got)
	}
}

func TestRecoverAnnotationJobsDoesNotExpireFreshPendingRetryFromOldJob(t *testing.T) {
	st := openAnnotationProgressTestStore(t)
	if err := st.CreateBackgroundJob(BackgroundJob{
		ID:             "fresh-retry",
		JobType:        "annotation_resume",
		Status:         "error",
		InputPayload:   "{}",
		CreatedAt:      100,
		TimeoutSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.IncrementBackgroundJobRetry("fresh-retry"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecoverExpiredAnnotationJobs(); err != nil {
		t.Fatal(err)
	}
	job, err := st.GetBackgroundJob("fresh-retry")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "pending" || job.StartedAt != nil {
		t.Fatalf("fresh pending retry was expired before Start: %+v", job)
	}
}

func exitedProcessPID(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows conservatively relies on deadline recovery")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAnnotationProgressExitedHelper$")
	cmd.Env = append(os.Environ(), "PINRU_EXITED_PROCESS_HELPER=1")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestAnnotationProgressExitedHelper(t *testing.T) {
	if os.Getenv("PINRU_EXITED_PROCESS_HELPER") != "1" {
		return
	}
	os.Exit(0)
}
