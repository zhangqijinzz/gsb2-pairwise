package annotation

import (
	domain "github.com/blueship581/pinru/internal/annotation"
	"testing"
)

func TestQuickExportRejectsConflictingCacheAndAcceptsLatestRepair(t *testing.T) {
	five, four := 5, 4
	bad := domain.Evaluation{EvidenceHash: "h", Status: "ready", Scores: [5]*int{&five, &five, &five, &five, &five}, Issues: []domain.Issue{{Kind: "bug"}}}
	c := domain.Case{TaskID: "t", Rounds: []domain.Round{{Status: "complete", EvidenceHash: "h", Evaluations: []domain.Evaluation{bad}}}}
	for _, reviewed := range []bool{true, false} {
		if _, err := selectExportCases([]domain.Case{c}, ExportRequest{ReviewedOnly: reviewed}); err == nil {
			t.Fatal("conflicting cached evaluation exported")
		}
	}
	good := bad
	good.Scores = [5]*int{&four, &four, &four, &four, &four}
	good.NextPrompt = "修复空值输入没有提示的问题"
	good.NextPromptType = "Bug修复"
	c.Rounds[0].Evaluations = append(c.Rounds[0].Evaluations, good)
	if _, err := selectExportCases([]domain.Case{c}, ExportRequest{ReviewedOnly: true}); err != nil {
		t.Fatal(err)
	}
}
