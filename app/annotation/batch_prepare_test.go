package annotation

import (
	"context"
	"testing"

	domain "github.com/blueship581/pinru/internal/annotation"
)

func TestBatchInputForcesRecaptureWhenLegacyRecoveryRoundsExist(t *testing.T) {
	service := &AnnotationService{}
	annotationCase := domain.Case{
		TracePath: "/tmp/session.jsonl",
		Captures:  []domain.Capture{{ID: "old-capture"}},
		Rounds: []domain.Round{
			{PromptID: "p-1", Prompt: "实现订单筛选功能", Status: "complete"},
			{PromptID: "p-continue-1", Prompt: "继续", Status: "complete"},
			{PromptID: "p-continue-2", Prompt: "请继续。", Status: "complete"},
		},
	}

	tracePath, resume, err := service.batchInput(context.Background(), annotationCase)
	if err != nil {
		t.Fatal(err)
	}
	if resume || tracePath != annotationCase.TracePath {
		t.Fatalf("batchInput() = %q, resume=%v; want live recapture from original trace", tracePath, resume)
	}
}

func TestFullyPreparedRequiresLegacyRecoveryRoundsToBeRecaptured(t *testing.T) {
	original := domain.Round{
		PromptID: "p-1", Prompt: "实现订单筛选功能", Status: "complete", EvidenceHash: "h-1",
		Evaluations: []domain.Evaluation{scoredEvaluation("h-1", [5]int{5, 4, 4, 4, 4})},
	}
	annotationCase := domain.Case{Rounds: []domain.Round{
		original,
		{PromptID: "p-continue", Prompt: "继续", Status: "complete"},
	}}
	if fullyPreparedWithoutRepair(annotationCase) {
		t.Fatal("legacy recovery row was treated as fully prepared")
	}
}
