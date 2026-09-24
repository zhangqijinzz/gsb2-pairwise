package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blueship581/pinru/internal/annotation"
)

func TestAnnotationCaseSaveLoadListPreservesEvaluationVersions(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	createAnnotationTask(t, s, "task-1")
	createAnnotationTask(t, s, "task-2")
	five := 5
	c := annotation.Case{
		TaskID: "task-1", ProjectID: "project-a", TaskName: "Demo", ContainerID: "container-1",
		Rounds: []annotation.Round{{PromptID: "p-1", EvidenceHash: "h-1", Evaluations: []annotation.Evaluation{
			{ID: "eval-1", EvidenceHash: "h-0", Scores: [5]*int{&five}},
			{ID: "eval-2", EvidenceHash: "h-1", Scores: [5]*int{&five}},
		}}},
	}
	saved, err := s.SaveAnnotationCase(c, 0)
	if err != nil {
		t.Fatalf("SaveAnnotationCase() error = %v", err)
	}
	if saved.Revision != 1 || saved.UpdatedAt <= 0 {
		t.Fatalf("saved metadata = revision %d, updatedAt %d", saved.Revision, saved.UpdatedAt)
	}
	loaded, err := s.GetAnnotationCase("task-1")
	if err != nil {
		t.Fatalf("GetAnnotationCase() error = %v", err)
	}
	if loaded == nil || len(loaded.Rounds) != 1 || len(loaded.Rounds[0].Evaluations) != 2 || loaded.Rounds[0].Evaluations[0].ID != "eval-1" {
		t.Fatalf("loaded case = %#v", loaded)
	}

	if _, err := s.SaveAnnotationCase(annotation.Case{TaskID: "task-2", ProjectID: "project-b"}, 0); err != nil {
		t.Fatal(err)
	}
	projectCases, err := s.ListAnnotationCases("project-a")
	if err != nil || len(projectCases) != 1 || projectCases[0].TaskID != "task-1" {
		t.Fatalf("ListAnnotationCases(project-a) = %#v, %v", projectCases, err)
	}
	allCases, err := s.ListAnnotationCases("")
	if err != nil || len(allCases) != 2 {
		t.Fatalf("ListAnnotationCases(all) len = %d, err = %v", len(allCases), err)
	}
}

func TestGetAnnotationCaseNormalizesHistoricalNullEvaluationsToJSONArray(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	createAnnotationTask(t, s, "task-legacy")
	if _, err := s.SaveAnnotationCase(annotation.Case{
		TaskID: "task-legacy",
		Rounds: []annotation.Round{{PromptID: "p-1", Evaluations: nil}},
	}, 0); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.GetAnnotationCase("task-legacy")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"evaluations":null`) || !strings.Contains(string(payload), `"evaluations":[]`) {
		t.Fatalf("historical case JSON = %s, want evaluations array", payload)
	}
}

func TestAnnotationCaseSaveLoadPreservesPairwiseData(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	createAnnotationTask(t, s, "task-pairwise")
	c := annotation.Case{
		TaskID: "task-pairwise",
		Mode:   annotation.CaseModePairwiseGSB,
		Pairwise: &annotation.PairwiseData{
			Prompt: "实现筛选功能",
			RunA:   annotation.PairwiseRun{Side: annotation.PairwiseSideA, Branch: "A", SessionID: "session-a"},
			RunB:   annotation.PairwiseRun{Side: annotation.PairwiseSideB, Branch: "B", SessionID: "session-b"},
		},
	}
	if _, err := s.SaveAnnotationCase(c, 0); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetAnnotationCase("task-pairwise")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != annotation.CaseModePairwiseGSB || loaded.Pairwise == nil || loaded.Pairwise.RunB.Branch != "B" {
		t.Fatalf("loaded pairwise case = %#v", loaded)
	}
}

func TestAnnotationCaseSaveUsesOptimisticRevision(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	createAnnotationTask(t, s, "task-1")
	first, err := s.SaveAnnotationCase(annotation.Case{TaskID: "task-1", TaskName: "first"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	updated := *first
	updated.TaskName = "updated"
	second, err := s.SaveAnnotationCase(updated, 1)
	if err != nil {
		t.Fatalf("update error = %v", err)
	}
	if second.Revision != 2 {
		t.Fatalf("updated revision = %d, want 2", second.Revision)
	}

	stale := *first
	stale.TaskName = "stale overwrite"
	if _, err := s.SaveAnnotationCase(stale, 1); !errors.Is(err, ErrAnnotationRevisionConflict) {
		t.Fatalf("stale SaveAnnotationCase() error = %v", err)
	}
	loaded, err := s.GetAnnotationCase("task-1")
	if err != nil || loaded.TaskName != "updated" || loaded.Revision != 2 {
		t.Fatalf("case after stale save = %#v, %v", loaded, err)
	}
}

func TestAnnotationCaseInitialSHABecomesImmutableAfterRoundExists(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	createAnnotationTask(t, s, "task-1")
	firstSHA := strings.Repeat("a", 40)
	secondSHA := strings.Repeat("b", 40)
	saved, err := s.SaveAnnotationCase(annotation.Case{TaskID: "task-1", InitialSHA: firstSHA}, 0)
	if err != nil {
		t.Fatal(err)
	}
	saved.InitialSHA = secondSHA
	saved, err = s.SaveAnnotationCase(*saved, saved.Revision)
	if err != nil {
		t.Fatalf("pre-turn SHA update error = %v", err)
	}
	saved.Rounds = []annotation.Round{{PromptID: "p", Status: "pending"}}
	saved, err = s.SaveAnnotationCase(*saved, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	saved.InitialSHA = firstSHA
	if _, err := s.SaveAnnotationCase(*saved, saved.Revision); !errors.Is(err, ErrAnnotationInitialSHAImmutable) {
		t.Fatalf("post-turn SHA update error = %v", err)
	}
}

func TestAnnotationCaseContainerBindingIsUniqueAcrossTasks(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	createAnnotationTask(t, s, "task-1")
	createAnnotationTask(t, s, "task-2")
	if _, err := s.SaveAnnotationCase(annotation.Case{TaskID: "task-1", ContainerID: "container-1"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAnnotationCase(annotation.Case{TaskID: "task-2", ContainerID: "container-1"}, 0); !errors.Is(err, ErrAnnotationContainerInUse) {
		t.Fatalf("duplicate container error = %v", err)
	}
	if _, err := s.SaveAnnotationCase(annotation.Case{TaskID: "task-2", ContainerID: ""}, 0); err != nil {
		t.Fatalf("empty container binding should be reusable: %v", err)
	}
}

func TestAnnotationCaseRequiresExistingTask(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	_, err := s.SaveAnnotationCase(annotation.Case{TaskID: "missing"}, 0)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("SaveAnnotationCase() error = %v, want foreign key error", err)
	}
}

func createAnnotationTask(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateTask(Task{ID: id, GitLabProjectID: 1, ProjectName: id}); err != nil {
		t.Fatalf("CreateTask(%s) error = %v", id, err)
	}
}
