package annotation

import (
	domain "github.com/blueship581/pinru/internal/annotation"
	"testing"
)

func TestProcessDeductionsDoNotGenerateRepairsOrChangeScores(t *testing.T) {
	five, four := 5, 4
	for _, tc := range []struct {
		name   string
		scores [5]*int
		count  int
		want   string
	}{
		{"all five", [5]*int{&five, &five, &five, &five, &five}, 1, ""},
		{"process deduction", [5]*int{&five, &five, &four, &five, &five}, 1, ""},
		{"missing evidence", [5]*int{&five, &five, nil, &five, &five}, 1, ""},
		{"round limit", [5]*int{&five, &five, &four, &five, &five}, 10, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &domain.Evaluation{Scores: tc.scores, Status: "ready", NextPrompt: "补充边界验证并说明结果", NextPromptType: "Bug修复", Issues: []domain.Issue{{Kind: "process", Description: "存在不必要的重复读取", Evidence: "原轨迹工具调用"}}}
			normalizeNextPrompt(e, tc.count)
			if e.Scores != tc.scores || e.NextPromptType != "" || len(e.Issues) != 1 {
				t.Fatal("process scores or evidence changed, or repair type retained")
			}
			if err := domain.ValidateRepairConsistency(*e); err != nil {
				t.Fatal(err)
			}
			if e.NextPrompt != tc.want {
				t.Fatalf("got %q want %q", e.NextPrompt, tc.want)
			}
		})
	}
}

func TestBugAdviceSurvivesRoundLimitAndContradictionsAreRejected(t *testing.T) {
	five, four := 5, 4
	e := &domain.Evaluation{Scores: [5]*int{&four, &five, &five, &five, &five}, Issues: []domain.Issue{{Kind: "bug"}}, NextPrompt: "修复空值提交无反馈的问题，应显示错误", NextPromptType: "Bug修复"}
	normalizeNextPrompt(e, 10)
	if err := domain.ValidateRepairConsistency(*e); err != nil {
		t.Fatal(err)
	}
	e.Scores[0] = &five
	normalizeNextPrompt(e, 1)
	if err := domain.ValidateRepairConsistency(*e); err == nil {
		t.Fatal("full scores with a bug must be rejected, not silently cleared")
	}
	e.Scores[0] = &four
	e.NextPrompt = ""
	normalizeNextPrompt(e, 1)
	if err := domain.ValidateRepairConsistency(*e); err == nil {
		t.Fatal("missing bug advice must be rejected")
	}
}
