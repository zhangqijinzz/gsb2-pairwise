package annotation

import "testing"

func TestRepairConsistency(t *testing.T) {
	five, four := 5, 4
	for _, tc := range []struct {
		name         string
		bug          bool
		delivery     *int
		prompt, kind string
		wantError    bool
	}{
		{"perfect", false, &five, "", "", false},
		{"low score without code defect", false, &four, "", "", false},
		{"low score cannot justify repair", false, &four, "修复重复检索", "Bug修复", true},
		{"perfect with advice", false, &five, "修复空值提示", "Bug修复", true},
		{"bug with perfect delivery", true, &five, "修复空值提示", "Bug修复", true},
		{"bug with missing delivery", true, nil, "修复空值提示", "Bug修复", true},
		{"bug without advice", true, &four, "", "", true},
		{"bug without prefix", true, &four, "补上空值提示", "Bug修复", true},
		{"bug without details", true, &four, "修复", "Bug修复", true},
		{"bug with wrong type", true, &four, "修复空值提示", "", true},
		{"bug with repair", true, &four, "修复空值提交无提示的问题，应显示错误信息", "Bug修复", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Evaluation{Scores: [5]*int{tc.delivery, &five, &five, &five, &five}, NextPrompt: tc.prompt, NextPromptType: tc.kind}
			if tc.bug {
				e.Issues = []Issue{{Kind: "bug"}}
			}
			if err := ValidateRepairConsistency(e); (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
}
