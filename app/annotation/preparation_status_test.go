package annotation

import (
	"context"
	"testing"
	"time"

	"github.com/blueship581/pinru/internal/store"
)

func TestListCasesRestoresPreparationFailureAndRuleFreshness(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	s.cli, _ = fakeReviewCLI(t)
	c, err := s.captureAndPrepareTable(context.Background(), CaptureRequest{TaskID: "题目-1", TracePath: trace})
	if err != nil {
		t.Fatal(err)
	}
	id := c.TaskID
	if err := s.store.CreateBackgroundJob(store.BackgroundJob{ID: "failed-review", JobType: "annotation_capture_table", TaskID: &id, Status: "running", CreatedAt: time.Now().Unix(), InputPayload: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpdateBackgroundJobProgress("failed-review", 15, "正在审核第 2 轮"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.FailBackgroundJob("failed-review", "执行超时"); err != nil {
		t.Fatal(err)
	}
	cases, err := s.ListCases("batch")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Preparation == nil || cases[0].Preparation.Status != "error" || cases[0].Preparation.LastActivityAt == 0 {
		t.Fatalf("missing persisted failure: %+v", cases)
	}
	if cur := cases[0].Rounds[0].Evaluations[0].Current; cur == nil || !*cur {
		t.Fatal("fresh evaluation marked stale")
	}
	c.Rounds[0].Evaluations[0].SkillHash = "old-rules"
	if _, err = s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	cases, err = s.ListCases("batch")
	if err != nil {
		t.Fatal(err)
	}
	if cur := cases[0].Rounds[0].Evaluations[0].Current; cur == nil || *cur {
		t.Fatal("old rule evaluation marked current")
	}
}

func TestExpiredPreparationRecoveryDoesNotEraseGrades(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	s.cli, _ = fakeReviewCLI(t)
	if _, err := s.captureAndPrepareTable(context.Background(), CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	id := "题目-1"
	if err := s.store.CreateBackgroundJob(store.BackgroundJob{ID: "interrupted", JobType: "annotation_resume", TaskID: &id, Status: "running", CreatedAt: 100, TimeoutSeconds: 1800, InputPayload: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.RecoverExpiredAnnotationJobs(); err != nil {
		t.Fatal(err)
	}
	job, err := s.store.GetBackgroundJob("interrupted")
	if err != nil || job.Status != "error" {
		t.Fatalf("job %+v %v", job, err)
	}
	c, err := s.store.GetAnnotationCase(id)
	if err != nil || len(c.Rounds[0].Evaluations) != 1 {
		t.Fatalf("lost results: %+v %v", c, err)
	}
}

func TestEarlierReconstructedRoundsRemainPreparedAfterReviewModelSettingChanges(t *testing.T) {
	s, trace, source := annotationFixture(t)
	writeFixtureTrace(t, trace, source, 2)
	s.cli, _ = fakeReviewCLI(t)
	if _, err := s.captureAndPrepareTable(context.Background(), CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	cases, err := s.ListCases("batch")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cases[0].Rounds {
		if cur := r.Evaluations[0].Current; cur == nil || !*cur {
			t.Fatalf("round %d incorrectly stale", r.Order)
		}
	}
	if err := s.store.SetConfig("annotation_review_model", "different-model"); err != nil {
		t.Fatal(err)
	}
	cases, err = s.ListCases("batch")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cases[0].Rounds {
		if cur := r.Evaluations[0].Current; cur == nil || !*cur {
			t.Fatalf("round %d lost prepared data after model setting change", r.Order)
		}
	}
}
